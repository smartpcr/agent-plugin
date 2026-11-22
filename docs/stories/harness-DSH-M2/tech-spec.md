---
title: "dsh M2 -- first prompt from the command line and the SDK"
storyId: "harness:DSH-M2"
---

# harness:DSH-M2 -- Technical specification (problem, scope, acceptance, risks)

> Story `harness:DSH-M2`: milestone **M2** of `dsh`, impl.md stages DH-S-12 to DH-S-18.
> It follows story `harness:DSH` (milestone M1, stages DH-S-1 to DH-S-11), whose
> [tech-spec.md](../harness-DSH-M1/tech-spec.md) is the template for this file.

This document states **what this story must achieve (part A), what it builds and in
what order (part B), and how completion is proven (part C)**, together with what is out
of scope, what is never done, the hard constraints and the risks. It is the contract the
story's later plan documents (an architecture, an implementation plan and an e2e
scenario script, written after this file and kept consistent with it) build against.

Source documents, which this story never re-decides: [design.md](../../harness/design.md)
(outcomes `DH-O-1..16`, owner decisions `DH-ADR-1..40`), [impl.md](../../harness/impl.md)
(stages, steps, rules, environments, scenario index), the area and library specifications
under `docs/harness/areas/` and `docs/harness/libs/`, and the ledger
[trace/ledger.jsonl](../../harness/trace/ledger.jsonl) (each deliverable's `proof` list).
The M1 story's IDs (NG-*, RM-*, QG-*, EV-*, SP-*, EN-*, DC-*, TC-*) are reused where they
still hold; IDs new to this story carry the prefix `M2-`.

---

## 1. Problem statement

### 1.1 What was asked, read precisely

| Phrase | Meaning adopted in this story | Source that fixes the meaning |
|---|---|---|
| "milestone M2" | The point at which [DH-S-17](../../harness/impl.md#dh-s-17) and [DH-S-18](../../harness/impl.md#dh-s-18) are **closed** and the guarded scenarios of [DH-S-16](../../harness/impl.md#dh-s-16) have passed. Not "the code exists" | impl.md [sec. 3.3](../../harness/impl.md#33-milestones-and-when-a-feature-may-be-enabled), M2 row |
| "first prompt from the command line and the SDK" | `dsh headless` answers a prompt; `dsh sdk` serves a client over stdio; a Go program drives the binary through the public client package in `src/sdk`; all against a real model API | impl.md DH-S-16 "Result"; M2 "What is usable" |
| "stages DH-S-12 to DH-S-18" | Five **build** stages (S12 files and policy, S13 sandbox and shell, S14 presets, S15 listener and SDK server, S16 launchers and evidence runners) and two **acceptance** stages (S17, S18) | impl.md [sec. 5](../../harness/impl.md#5-stage-index) |
| "executes scenarios" | Unlike M1, this story **runs** real-environment `DH-E-*` scenarios: 4 guarded ones inside S16 (step 16.8), 14 in S17, 19 in S18 -- **37 in all** | impl.md [sec. 7.5](../../harness/impl.md#75-index-of-all-scenarios) |
| "nothing is enabled" | M2 gives the project team a usable pre-release on Linux; no feature is switched on for users | sec. 4.7 below |

### 1.2 The problem in one paragraph

M1 left a spine that runs one turn inside a Go test: no binary a person can start, no
place where model-chosen commands may run, and no evidence from outside a test double.
The problem of this story is to **turn that spine into a binary that answers a prompt
from the command line and from an SDK program, runs model-chosen file edits and shell
commands inside a fence and a Linux sandbox, and then prove, on real machines and
against a real model, that the M1 guarantees and the new execution world hold** --
without enabling anything for users, and without letting a defect in takeover, deletion
or process-tree ownership touch anything but scratch data.

### 1.3 Why this cut, and why it is hard

- **It is the first real evidence for the spine.** Every M1 deliverable whose proof
  includes a scenario is still only *built*. S17 is the first stage where turn
  guarantees (DH-O-3), logged requests (DH-O-4), honest recovery (DH-O-5) and the tool
  pipeline (DH-O-6) are shown against a real model.
- **It is the first point at which model-chosen commands run** (DH-S-13 "Result"). A
  fence or sandbox defect writes outside the user's roots; a process-tree defect leaves
  processes behind. These are security and ownership claims about real operating
  systems (DH-O-7, DH-O-10), and only real machines can prove them.
- **The environments are the long pole.** S18 alone needs about a dozen machine variants
  (sec. 4.3), several of which hosted CI runners do not offer. The code for S12 to S16
  is ordinary work; arranging the targets is not.
- **It starts from an unclosed M1** (sec. 1.5), so the first obligation is not M2 work.

### 1.4 Vocabulary used in this story

| Term | Plain meaning | Why it matters here |
|---|---|---|
| Execution world | The working directory, the filesystem seam, the policy resolver, the sandbox and the shell: everything a tool needs to touch the machine | S12 and S13 build it; S18 proves it |
| Fence, write roots | The filesystem seam refuses any path outside the session's write roots, including through symbolic links swapped during a write | The one place "may this call write here" is answered (DH-D-FS-4) |
| Policy resolver | One component that turns the user's Permissions choice (read-only, workspace-write, full access) into the policy for one call | Every enforcing capability asks it (DH-D-SANDBOX-1) |
| Confinement backend | The OS mechanism that confines a command: **bubblewrap** (`bwrap`, a program that starts a command in a restricted file-system view) or **Landlock** (a kernel feature by which a process limits its own writes) | Linux only at M2; macOS and Windows backends are P1 (impl.md G-3) |
| Enforcement `full` / `partial` | What the sandbox reports it actually enforces, with each exposure named; shown by `dsh sandbox status` | A partial sandbox is reported, never hidden |
| Process-tree owner | What lets the host end a command and all its descendants: a **delegated scope** from the user service manager, a writable **control group**, or the process-group fallback | Proven only on real Linux (DH-E-EXEC-1, -10) |
| Profile, preset, bundle | A profile is a product shape of the one binary (`headless`, `sdk`, `web`, ...); a bundle is a set of components; a preset composes one agent's tools and prompt | S14, step 16.1 |
| Launcher, admin tree | `dsh headless`, `dsh sdk` start a profile; the admin tree runs offline commands (`dsh secret set`, `dsh config show`) beside a live host | S16 |
| SDK server, stdio | A client program drives sessions with JSON-RPC over the host's standard input and output | S15 (DH-O-13) |
| Local test, e2e test, manual check, scenario | See sec. 4.1: four kinds of evidence; **only a scenario proves a real environment** | EV-1 |
| Guarded scenario | One whose defect could delete, restore over or take ownership of something; runs inside its build stage on scratch data | Step 16.8 |
| Controller, target, variant | The controller runs `go test` and drives the run; a target is a machine of one variant (for example "Linux, Landlock only") | sec. 4.3 |
| Built / done | Built: code merged, local tests pass, manual check recorded. Done: built, held-open items closed, every proving scenario passed | impl.md [sec. 1.3](../../harness/impl.md#13-vocabulary); never merged |

### 1.5 Starting point in the repository (verified on this branch)

| Fact | Value today | Consequence |
|---|---|---|
| DH-S-11 | **Not closed.** The real-DeepSeek M1 manual check has not run; steps 11.3, 11.4, 11.7 unticked; ticking and closure deferred to a follow-up PR ([DH-S-11.md](../harness-DSH-M1/evidence/DH-S-11.md), decision `m1-operator-run`) | M2-DEC-1: closing M1 is an entry prerequisite |
| DH-S-8, DH-S-9 | Also open: steps 8.3-8.5 wait for Stage 8.2's closure; steps 9.1-9.3 wait for their manual check ([DH-S-8.md](../harness-DSH-M1/evidence/DH-S-8.md), [DH-S-9.md](../harness-DSH-M1/evidence/DH-S-9.md)) | DH-S-12 needs S8 built; DH-S-11 needs S9 |
| Gate runner | `TestRepositoryPassesEveryGate` fails at its vet gate on files of earlier stages; fix deferred by decision `inherited-vet-gate-failure` | QG-1/QG-2 allow no baseline: must be green before M2 closes any stage |
| Model API reachability | From the authoring host `curl` to `https://api.deepseek.com/` times out (DH-S-9 and DH-S-11 evidence) | Risk M2-R-01 |
| [acceptance-index.yaml](../../../test/harness/acceptance-index.yaml) | `built-through: DH-S-7`. Of the 37 scenarios M2 executes, 23 are authored: 3 of 4 for 16.8, 12 of 14 for S17, 8 of 19 for S18 | 14 remain to author (sec. 4.2) |
| [harness-scenarios.yml](../../../.github/workflows/harness-scenarios.yml) | The credentialed workflow (DH-S-1 step 1.8): `workflow_dispatch` and schedule only; `preflight` job derives `DSH_REQUIRE_SCENARIOS` from the index and fails on a missing key or a missing scenario test; `scenarios` job runs **every** required test with `go test` from source on `ubuntu-latest`, `macos-latest`, `windows-latest`; secret `DSH_DEEPSEEK_API_KEY` | Adequate for M1; not for M2 (M2-DEC-2, -3, -4) |
| `e2e-*.yml` workflows | Keyless godog suites (`-tags e2e`) on hosted runners, one per M1 phase | Local integration evidence, not scenarios (sec. 4.1) |

---

## 2. Outcomes (part A)

Each story outcome is an observable result, linked to the design outcome it serves, the
stages and deliverables that produce it (part B) and the scenarios that prove it (part
C). "Done at M2" says whether the outcome's deliverables can reach *done* in this story
or stay *built* because a proving scenario runs later (impl.md
[sec. 10.4](../../harness/impl.md#104-scenarios-that-span-stages), S-1).

| ID | Outcome (observable) | Design outcome | Produced by | Proven by (executed here) | Done at M2 |
|---|---|---|---|---|---|
| M2-O-1 | Every tool of a session works in one directory; a write outside the write roots is refused, even through a link swapped mid-write; the six file tools edit real files as logged | DH-O-7, DH-O-10 | S12 | DH-E-FS-1, -2, -3 (guarded), DH-E-VIEW-5, DH-E-SANDBOX-7 | Partly: FS-4 waits for DH-E-FS-5 (S28) |
| M2-O-2 | A model-chosen command starts under its resolved policy on Linux, confined by bubblewrap or Landlock with enforcement reported, or does not start; its whole process tree is owned and ended; instruction files reach the model | DH-O-7, DH-O-1, DH-O-6 | S13 (and S6's EXEC-2) | DH-E-SANDBOX-1, DH-E-NATIVESYSTEM-1..4, DH-E-EXEC-1, -10, DH-E-CONTEXT-2 | Partly: most S13 deliverables wait for S32/S39 |
| M2-O-3 | One binary carries five profiles and its bundles; a preset composes an agent; a coverage check fails on an unbundled component | DH-O-2 | S14, step 16.1 | None in M2 (local tests; scenarios run in S22, S48) | No (built only) |
| M2-O-4 | A Go program drives sessions over stdio: create, resume, prompt, cancel, close, read, and answer approvals and questions | DH-O-13 | S15, step 16.6 | DH-E-SDK-1 | Partly: SDK-4/-5 held open (H-1..H-4) |
| M2-O-5 | `dsh headless` and `dsh sdk` start; stopping is one bounded path; a second launch finds the host; a crash is explained at the next start; a key is stored by `dsh secret set`; `dsh config show/validate` work | DH-O-14, DH-O-3 | S16 | DH-E-HOST-4, DH-E-STORE-1, -2, -6 (guarded) | Partly: CLI-2/-4/-5 wait for DH-E-CLI-1, -2 (S27) |
| M2-O-6 | A recorded session replays without a key against the built binary; a run states which evidence it holds; a required scenario that skips fails | DH-O-15 | step 16.7 | DH-E-TOOLCHAIN-1, -2 | Partly: UTIL-7 held open (H-10); UTIL-8 waits for DH-E-UTIL-2 (S39) |
| M2-O-7 | Against a real model on Linux the built binary keeps the turn guarantees, sends exactly what the log says, recovers honestly from a kill and follows a configuration edit | DH-O-3, -4, -5, -6, -8, -2 | M1 stages S2..S11, S14..S16 | S17: DH-E-SESSION-1, -2, DH-E-AGENT-1..4, DH-E-LLM-1, -3, DH-E-TOOLS-1, DH-E-VIEW-1, DH-E-LOADER-1 | For the deliverables those scenarios finish |
| M2-O-8 | The M1 foundations hold on real operating systems: release archives run, files are watched and searched, a file is replaced durably under power loss, stored data is private, a dead host leaves a report | DH-O-14, DH-O-5, DH-O-1 | M1 stages S1, S4, S6 | S18: DH-E-TOOLCHAIN-2, DH-E-HOST-4, DH-E-RIPGREP-1, -2, DH-E-CHOKIDAR-1, -2, DH-E-NODEFS-1, DH-E-STORAGE-6 | For the deliverables those scenarios finish |

---

## 3. In scope (part B)

### 3.1 Entry prerequisite: M1 closed (M2-DEC-1)

**Choice.** No M2 build stage starts until DH-S-8, DH-S-9 and DH-S-11 are closed:
their manual checks recorded with a real key (DH-S-9, and the two M1 runs of DH-S-11),
Stage 8.2's closure landed, steps 8.3-8.5, 9.1-9.3, 11.3, 11.4, 11.7 ticked,
`built-through` raised to DH-S-11, and the gate runner green on Linux, macOS and Windows.
**Why.** impl.md rule 1: a build stage starts when every stage on its Entry line is
built, and "built" means its exit evidence is recorded. S12 names S8, S10, S11; S13
names S6, S11, S12. **Tradeoff.** M2 waits for an operator run on a network that reaches
`api.deepseek.com`; starting S12 on an open S11 would be faster and is ruled out by
rule 1. **Supports** every M2 outcome. **Verified by** the closed evidence records and
the index's `built-through` value at the first M2 commit.

### 3.2 Stages

Step counts are impl.md's. The implementation plan may use at most 20 checklist steps
per plan stage; M1 split each impl.md stage into plan sub-stages, and the last column
flags where M2 will need the same.

| Stage | Result (condensed from impl.md) | Priority | Entry (impl.md) | Steps | Deliverables | Exit evidence | Plan split |
|---|---|---|---|---|---|---|---|
| [DH-S-12](../../harness/impl.md#dh-s-12) | One working directory, one fenced filesystem seam, one policy resolver, six file tools | P0 (1 P1) | S8, S10, S11 | 5 | 5 | Evidence record; the three circle members of C-3 (VIEW-10, FS-4, SANDBOX-1) built at one commit | Not needed (C-3 is one unit) |
| [DH-S-13](../../harness/impl.md#dh-s-13) | A command starts under its policy or not at all; Linux backends; `bash`/`pwsh` tools; instruction files | P0 | S6, S11, S12; Linux hosts with bwrap, Landlock only, neither, old kernel | 8 | 8 | Fake-backend tests of every security control; real mechanism proven only in S18 | **Likely**: launch and backends vs executors and tools |
| [DH-S-14](../../harness/impl.md#dh-s-14) | Profiles and bundles as data; preset registry; shipped presets; coverage check | P0 | S6, S11 | 5 | 5 | Coverage check passing, optional list reviewed | Not needed; may run beside S12/S13 |
| [DH-S-15](../../harness/impl.md#dh-s-15) | Listener with launch-token admission, reused host, declared routes; one owner of stdio; SDK methods, results, answers | P0 | S6, S8, S11, S12, S14 | 8 | 8 | List of complete SDK methods and those waiting for H-1..H-4 | **Likely**: listener half vs stdio half |
| [DH-S-16](../../harness/impl.md#dh-s-16) | Mode layers and five profiles; admin tree; launchers; bounded stop; second launch; Go client; replay and scenario helper; titles; close H-21; guarded scenarios | P0 (4 P1) | S12..S15; model key for the manual check | 10 | 9 + 1 scenario batch + 1 closing step | `DH-T-TOOLCHAIN-1, -3, -5, -6` pass; cleanup evidence of 16.8; open list H-1..H-4, H-10 | **Required**: at least launchers, evidence runners, and the 16.8 batch |
| [DH-S-17](../../harness/impl.md#dh-s-17) | Core loop, store, launchers and SDK against a real model on Linux | P0 | S16 built with 16.8 passed; E1, E2 Linux; key at run time | 9 | -- | Per-scenario record: binary identity, provider and model id, run ids, session exports, "request received equals request rebuilt" | Not needed |
| [DH-S-18](../../harness/impl.md#dh-s-18) | Execution world on real OSes: process trees, Landlock, fence, watch, search, crash report, `dsh sandbox status` | P0 | S16 built; E2 variants, logging proxy, power-off VMs, network share | 11 | -- | Record per scenario **and per OS variant**, with reported enforcement; a missing variant leaves its scenario open | **Likely**: by environment group (hosted runners, Linux variant VMs, power-off VMs) |

**Size facts** (no point estimate; see M2-Q-1): 7 stages (5 build, 2 acceptance), 56
impl.md steps, 35 deliverables (30 P0, 5 P1 pulled forward: HUMAN-4, SDK-8, UTIL-7,
UTIL-8, VIEW-4), 37 scenarios executed, 32 scenario files to author (14 executed here,
18 later), 1 held-open item closed (H-21) and 5 carried open (H-1..H-4, H-10).

### 3.3 Deliverables, outcome and proof

Every deliverable maps to one story outcome and to its proof. "Local" means the ledger
names local tests only, so the deliverable is done when built (impl.md G-5). The `DH-D`
links resolve in the area specification named by impl.md for each step.

| Step | Deliverable | Outcome | Proving scenarios (ledger `proof`) and where they run | Done at M2 if all M2 scenarios pass |
|---|---|---|---|---|
| 12.1 | DH-D-VIEW-10 one working directory | M2-O-1 | DH-E-VIEW-5 (S18) | Yes |
| 12.2 | DH-D-FS-4 fenced filesystem seam | M2-O-1 | DH-E-FS-1 (S18), DH-E-FS-3 (16.8), DH-E-FS-5 (S28) | No (S28) |
| 12.3 | DH-D-SANDBOX-1 policy resolver | M2-O-1 | DH-E-SANDBOX-7 (S17) | Yes |
| 12.4 | DH-D-HUMAN-4 Permissions choice (P1, pulled forward) | M2-O-1 | Local | Yes |
| 12.5 | DH-D-FS-6 six file tools | M2-O-1 | DH-E-FS-2 (S18) | Yes |
| 13.1 | DH-D-SANDBOX-2 launch stage | M2-O-2 | DH-E-SANDBOX-1 (S18), DH-E-SANDBOX-2 (S39) | No (S39) |
| 13.2 | DH-D-EXEC-4 start under launch plan or not at all | M2-O-2 | DH-E-EXEC-8 (S39) | No |
| 13.3 | DH-D-SANDBOX-3 probe, select, prepare, confirm, report | M2-O-2 | DH-E-SANDBOX-2 (S39) | No |
| 13.4 | DH-D-SANDBOX-4 bubblewrap, then Landlock | M2-O-2 | DH-E-SANDBOX-1, with DH-E-NATIVESYSTEM-1..4 (S18) | Yes |
| 13.5 | DH-D-EXEC-3 bounded output capture | M2-O-2 | Local | Yes |
| 13.6 | DH-D-EXEC-8 bash and PowerShell executors | M2-O-2 | DH-E-EXEC-7 (S39) | No |
| 13.7 | DH-D-EXEC-9 one-shot `bash` and `pwsh` tools | M2-O-2 | DH-E-EXEC-8 (S39) | No |
| 13.8 | DH-D-CONTEXT-2 instruction files | M2-O-2 | DH-E-CONTEXT-2 (S18), DH-E-CONTEXT-1 (S32) | No (S32) |
| 14.1 | DH-D-BUNDLE-1 profile content and membership | M2-O-3 | Local | Yes |
| 14.2 | DH-D-BUNDLE-2 host-plane layers | M2-O-3 | DH-E-BUNDLE-3 (S22, see M2-I-2) | No |
| 14.3 | DH-D-BUNDLE-4 preset registry | M2-O-3 | DH-E-BUNDLE-2 (S48) | No |
| 14.4 | DH-D-BUNDLE-5 shipped presets | M2-O-3 | DH-E-BUNDLE-2 (S48) | No |
| 14.5 | DH-D-BUNDLE-6 coverage check | M2-O-3 | DH-E-BUNDLE-4 (S48) | No |
| 15.1 | DH-D-HOST-1 listener | M2-O-5 | DH-E-HOST-2 (S22) | No |
| 15.2 | DH-D-HOST-2 browser admission | M2-O-5 | DH-E-HOST-1, -2 (S22); DH-E-HOST-7, -8, -9 (S39) | No |
| 15.3 | DH-D-HOST-3 UI shown, host reused | M2-O-5 | DH-E-HOST-1 (S22) | No |
| 15.4 | DH-D-API-1 declared routes | M2-O-5 | DH-E-API-5 (S48) | No |
| 15.5 | DH-D-SDK-3 one owner of stdio, bounded exit | M2-O-4 | DH-E-SDK-2 (S39) | No |
| 15.6 | DH-D-SDK-4 session methods (held open H-1..H-3) | M2-O-4 | DH-E-SDK-1 (S17), DH-E-SDK-2 (S39) | No |
| 15.7 | DH-D-SDK-5 results and notifications (held open H-4) | M2-O-4 | DH-E-SDK-1 (S17) | No (H-4, step 30.7) |
| 15.8 | DH-D-SDK-6 answers over the connection | M2-O-4 | DH-E-SDK-1 (S17) | Yes |
| 16.1 | DH-D-BUNDLE-3 mode layers, five profiles | M2-O-3 | DH-E-BUNDLE-1, -4 (S48), DH-E-BUNDLE-3 (S22) | No |
| 16.2 | DH-D-CLI-9 admin tree, `dsh secret set` | M2-O-5 | Local | Yes |
| 16.3 | DH-D-CLI-2 profile launchers | M2-O-5 | DH-E-CLI-1, -2 (S27) | No |
| 16.4 | DH-D-CLI-4 one bounded stop path | M2-O-5 | DH-E-CLI-1, -2 (S27) | No |
| 16.5 | DH-D-CLI-5 second launch, crash explained | M2-O-5 | DH-E-CLI-1 (S27) | No |
| 16.6 | DH-D-SDK-8 Go client in `src/sdk` (P1, pulled forward) | M2-O-4 | DH-E-SDK-1 (S17) | Yes |
| 16.7 | DH-D-UTIL-8 scenario helper; DH-D-UTIL-7 replay (both P1; UTIL-7 held open H-10) | M2-O-6 | DH-E-UTIL-2 (S39); DH-E-TOOLCHAIN-1 (S17) | No (S39; H-10 at 29.7) |
| 16.8 | Guarded batch: DH-E-FS-3, DH-E-STORE-1, -2, -6 | M2-O-1, M2-O-5 | Runs here (sec. 4.2) | Finishes STORE-2, -3, -4, -6 of S7 |
| 16.9 | DH-D-VIEW-4 titles (P1, pulled forward) | M2-O-5 | DH-E-VIEW-1 (S17) | Yes |
| 16.10 | Close H-21: DH-D-BOOT-6 via `dsh config show`, `dsh config validate` | M2-O-5 | Its waiting local tests | Closes H-21 |

So of the 35 deliverables, **11 can be done at M2** (7 by scenarios, 4 local-only) and
**24 stay built, not done**, each with its finishing stage named above.

### 3.4 Work products in scope

1. **Code** for the 35 deliverables, including the five pulled forward; their evidence
   bar is unchanged (impl.md sec. 4).
2. **Local tests** named in each deliverable's "Done when" line, under the gate runner,
   with the 85% coverage floor (QG-3).
3. **Scenario files**: the 14 executed here and not yet authored, and the 18 authored
   here for later stages (sec. 4.6), each listed in the acceptance index under its
   executing stage (impl.md rule 4).
4. **Manual checks**, one per step, labelled `manual`, as each stage's "Manual checks"
   line names them (S13 and S16 need real Linux and the real API).
5. **Acceptance harness work** that lets the existing credentialed workflow run M2
   scenarios: per-scenario targets, one built binary, target inventory, reports
   (M2-DEC-2 to -5). This is repository tooling around DH-D-UTIL-8 and DH-D-TOOLCHAIN-4,
   not a new deliverable ID (DC-5).
6. **Evidence records**, one per stage, in the format of impl.md
   [sec. 7.3](../../harness/impl.md#73-the-evidence-record), under
   `docs/stories/harness-DSH-M2/evidence/DH-S-12.md` to `DH-S-18.md`.
7. **Ledger upkeep**: `tools/harness-trace verify` stays green; deliverables move to
   *built*, and to *done* only by the rule of sec. 3.3.

### 3.5 Delivery order and parallelism

1. **At story start, in parallel** (long pole first): close M1 (M2-DEC-1); provision the
   acceptance targets of sec. 4.3 and confirm that the credentialed runners reach the
   model API; extend the workflow (M2-DEC-2 to -4) against what is already committed,
   using the 23 authored scenarios as its first payload.
2. **S12, with S14 beside it** (S14 needs only S6 and S11). Then **S13** (needs S12).
3. **S15** (needs S12 and S14). Then **S16**, whose step 16.8 runs the four guarded
   scenarios on scratch homes; S16 does not close until they pass.
4. **S17 and S18** may run in parallel once S16 is built. In S17 the keyless replay
   step runs first, so a wrong build is found before a credential is spent; in S18 the
   release rehearsal runs first and the power-off scenario last.

Dependency kinds stay apart (impl.md sec. 3.4): stage entry lines are implementation
dependencies; bubblewrap, Landlock, a user service manager and a model key are runtime
prerequisites that block a scenario, never a build stage; "enabled for users" is an
operator lifecycle matter that M2 does not reach.

### 3.6 Status model

Five statuses are kept apart per stage (impl.md sec. 1.2). Today:

| Stage | Design | Implementation | Test authoring | Local passes | Real-environment acceptance |
|---|---|---|---|---|---|
| S12..S16 | Done (documents) | Not started | 3 of 4 guarded scenarios authored (STORE-1, -2, -6); others not | Not started | 16.8: 0 of 4 run |
| S17 | Done | n/a | 12 of 14 authored (SDK-1, SANDBOX-7 missing) | n/a | 0 of 14 run |
| S18 | Done | n/a | 8 of 19 authored | n/a | 0 of 19 run |

A build stage closes as **built**; an acceptance stage closes only when every indexed
scenario passed on every variant it names. A scenario recorded as skipped, blocked, not
run or timed out is **open** (impl.md rules 5, 6).

### 3.7 Definition of done for this story

The story is done when **all** of these hold:

- **M2-DOD-1** M1 closed as in M2-DEC-1.
- **M2-DOD-2** Steps 12.1 to 16.10 ticked as *built*; `tools/harness-gate` green on the
  three CI operating systems at the closing commit of each build stage.
- **M2-DOD-3** The four guarded scenarios of step 16.8 passed, with the cleanup evidence
  impl.md names (scratch homes and the file compared; deleted sessions' rows, blobs and
  spill shown gone; `seq` 0 to n with no gap).
- **M2-DOD-4** All 14 S17 scenarios and all 19 S18 scenarios passed on every variant
  they name, each with its evidence record; S17 and S18 closed. This is milestone M2.
- **M2-DOD-5** H-21 closed at 16.10; H-1..H-4 and H-10 listed open with their closing
  steps; the 24 built-not-done deliverables listed with their finishing stages.
- **M2-DOD-6** The 18 later-stage scenarios authored and indexed (sec. 4.6).

---

## 4. Acceptance (part C)

### 4.1 Kinds of evidence, and which count

| Kind | How it runs | What it proves | Ticks a scenario? |
|---|---|---|---|
| Local test (`DH-T-*`) | Gate runner, keyless, three OSes; test kit fakes, `t.TempDir()` homes | The deliverable's contract in process (EV-1, EV-5) | No |
| e2e test (`-tags e2e`, the `[e2e]` PRs) | Godog suites in `e2e-*.yml` on hosted runners, keyless | Composition through the real loader and real SQLite | No: it is local integration evidence |
| Keyless replay (DH-D-UTIL-7) | A recorded session replayed against the built binary | Behaviour without a credential | No (local evidence) |
| Manual check | A person runs the piece once and records it, `manual` | Catches what a test did not look for | No (EV-3) |
| Real-environment scenario (`DH-E-*`, `-tags scenario`) | Credentialed workflow; real model, real OS, real sandbox | The stated contract on the tested build, inputs and environments | **Yes, the only kind** |

### 4.2 Scenarios executed in this story

"Authored" refers to [acceptance-index.yaml](../../../test/harness/acceptance-index.yaml)
today. Key: E1 = real model API, key at run time; "replay" = the replay provider, no key.

| Step | Scenarios | Proves (deliverables) | Target variants | Model | Authored |
|---|---|---|---|---|---|
| 16.8 (guarded) | DH-E-FS-3 | FS-4 | Linux, macOS, Windows; two `dsh` processes, an editor-save script | none | **No** |
| 16.8 (guarded) | DH-E-STORE-1 | STORE-2, STORE-3 | Linux, macOS, Windows | replay | Yes (S7) |
| 16.8 (guarded) | DH-E-STORE-2 | STORE-4, STORE-2 | Linux with a sandbox backend (see M2-I-5) | E1 | Yes (S7) |
| 16.8 (guarded) | DH-E-STORE-6 | STORE-6 | Linux, macOS, Windows | replay | Yes (S7) |
| 17.1 | DH-E-TOOLCHAIN-1 | TOOLCHAIN-3, -4, UTIL-7 | Hosted Linux | E1, then keyless | Yes |
| 17.2 | DH-E-SESSION-1, -2 | SESSION-1 to -5 | Linux; SESSION-2 with sandboxed shell | E1 | Yes |
| 17.3 | DH-E-AGENT-1..4 | AGENT-3, -5, -6 | Linux; AGENT-3 with sandboxed shell | E1 | Yes |
| 17.4 | DH-E-LLM-1, -3 | LLM-1, -2, -3, -6 | Linux; recording proxy; LLM-3 needs a second working route (M2-Q-7) | E1 | Yes |
| 17.5 | DH-E-TOOLS-1 | TOOLS-2, -5 | Linux | E1 | Yes |
| 17.6 | DH-E-VIEW-1 | VIEW-4 | Linux | E1 | Yes |
| 17.7 | DH-E-LOADER-1 | LOADER-2, -7, -11 | Linux | E1 | Yes |
| 17.8 | DH-E-SDK-1 | SDK-4, -5, -6, -8 | Linux; Go test program on `src/sdk` | E1 | **No** |
| 17.9 | DH-E-SANDBOX-7 | SANDBOX-1 (policy text); closes debt item `UP-DEBT-found.Q3.2` | Linux; three platform texts read from the resolver | E1 (108 runs) | **No** |
| 18.1 | DH-E-TOOLCHAIN-2 | TOOLCHAIN-4, -5, -6 | Hosted Linux, macOS, Windows | keyless replay | Yes |
| 18.2 | DH-E-HOST-4 | HOST-7 | Linux, macOS, Windows | none | Yes |
| 18.3 | DH-E-EXEC-1 | EXEC-2 | Linux cgroup v2 writable; a container without systemd | none | Yes |
| 18.3 | DH-E-EXEC-10 | EXEC-2 (exact ownership, U-K1-1; fact F-6) | Linux with systemd over a real SSH login; a second machine with the user manager stopped | none | **No** |
| 18.4 | DH-E-NATIVESYSTEM-1..4 | SANDBOX-4 (library rows) | Landlock ABI >= 5 without bwrap; VMs with ABI 1-4; Landlock disabled; glibc and musl; both build modes | none | **No** |
| 18.4 | DH-E-SANDBOX-1 | SANDBOX-2, -3, -4 | (a) bwrap; (b) Landlock only; (c) old kernel | none | **No** |
| 18.5 | DH-E-FS-1 | FS-4 | ext4, APFS, NTFS | none | **No** |
| 18.5 | DH-E-FS-2 | FS-6 | Linux; a public repo checkout, no `rg` | E1 | **No** |
| 18.6 | DH-E-VIEW-5 | VIEW-10 | Linux with git worktree and sandbox | E1 | **No** |
| 18.7 | DH-E-RIPGREP-1, -2 | FS-3 (library rows) | Linux, macOS, Windows; small/medium/large repos; `rg` for comparison | none | Yes |
| 18.8 | DH-E-CHOKIDAR-1, -2 | UTIL-3 (watcher; library rows) | Three OSes with real editors; an SMB share, an NFS mount, WSL | none | Yes |
| 18.9 | DH-E-CONTEXT-2 | CONTEXT-2 | Linux, macOS, Windows host; real editor, git | E1 (prompts) | **No** |
| 18.10 | DH-E-STORAGE-6 | STORAGE-2 | Three OSes, two ordinary user accounts each | none | **No** (see M2-I-10) |
| 18.11 | DH-E-NODEFS-1 | FS-1 (library rows) | One power-off-capable VM per OS plus a recording machine | none | Yes |

Each scenario's Given, When, Then and Cleanup are in its specification; this table is
an index, not a restatement.

### 4.3 Acceptance environments

| ID | Requirement |
|---|---|
| M2-EN-1 | **Targets.** E1: the `deepseek-official` route, reachable from the runner that executes the scenario. E2: hosted Linux, macOS and Windows runners for TOOLCHAIN-1/-2, RIPGREP, CHOKIDAR-1, HOST-4, STORE-1/-6, FS-1/-3, CONTEXT-2 where they suffice; team-kept virtual machines for every Linux variant hosted runners lack (Landlock only without bwrap, old kernels at Landlock ABI 1-4, Landlock disabled, musl, systemd with SSH login, no user service manager, writable cgroup v2), for the two-account machines of STORAGE-6, for the three power-off VMs of NODEFS-1, and for the SMB/NFS/WSL setups of CHOKIDAR-2 |
| M2-EN-2 | **Discovery.** A committed target inventory names each target by role and how it is reached (runner label or SSH host alias), never by address, user or key. Before any scenario, preflight probes every target and records its facts: OS and version, kernel, Landlock ABI, `bwrap` presence, cgroup writability, `systemd --user`, filesystem type, accounts. A probe that contradicts the role fails preflight (M2-DEC-4) |
| M2-EN-3 | **Transport.** The controller (`go test -tags scenario`) runs on the target, or on a controller machine that reaches the VM over SSH (impl.md sec. 7.1, E2); the controller may be a developer machine. The model API is reached from the target through egress under an explicit permit (SP-13) |
| M2-EN-4 | **Credentials at run time only.** The model key as the job secret `DSH_DEEPSEEK_API_KEY`, in the child's environment only; the SSH private key for the VM controller; share credentials for CHOKIDAR-2; the second account of STORAGE-6, created on a snapshot per run; a proxy certificate created per run. None is committed, written to a layer file, echoed into a record or present in the keyless jobs (EN-3, SP-11) |
| M2-EN-5 | **Workflow and preflight.** Runs start from `harness-scenarios.yml` by manual dispatch with the stage, or on its schedule; never from a pull request or a fork (QG-10). Preflight (DH-S-1 step 1.8) derives `DSH_REQUIRE_SCENARIOS` with `harness-gate require --stage`, and fails before any scenario starts when the key is missing, a required ID has no scenario test, `built-through` has not reached a scenario's `requires`, or a required target is unreachable |
| M2-EN-6 | **Time limits.** impl.md sec. 7.2 starting values (Inference): E1 15 min per scenario, 2 h per job; E2 20 min per scenario including snapshot restore, 3 h per variant. A scenario at its deadline has **failed**. SANDBOX-7, NODEFS-1 and FS-2 do not fit these values (M2-I-12, M2-Q-5) |
| M2-EN-7 | **Reports.** The scenario helper (DH-D-UTIL-8) writes one record per scenario and variant with the sec. 7.3 fields; the job publishes one report per acceptance stage as a CI artifact; the stage's evidence file cites run and job ids, scenario ids, session ids and provider request ids, and for S18 the reported enforcement and each exposure |
| M2-EN-8 | **Recovery and cleanup.** Each scenario runs its own Cleanup. If cleanup fails, the run keeps the scratch home, the fatal report, the process list and the session exports, marks the scenario failed, and restores the baseline: VM snapshot restored, or scratch user and home removed; the provider keeps only usage |

### 4.4 Story-level decisions for acceptance

| ID | Choice | Why | Tradeoff | Supports | Verified by |
|---|---|---|---|---|---|
| M2-DEC-2 | Each indexed scenario declares the target variants it runs on; the workflow runs it only there, once per variant | The current workflow runs every required test on three hosted OSes; a Linux-only scenario would fail elsewhere, and most S18 variants do not exist on hosted runners (impl.md sec. 7.1 E2) | More index metadata and a target map instead of one fixed matrix | M2-O-1, -2, -8 | Preflight refuses a scenario with no declared or reachable target; a local test of the index reader |
| M2-DEC-3 | Build `dsh` once per platform from the commit under test, record its checksum and version, copy it to targets and pass it as `DSH_BIN` | impl.md sec. 7.2 step 1 and DH-ADR-TOOLCHAIN-1: a scenario never builds its own binary; today the workflow runs scenarios from source | Adds an artifact hand-off step | All | Each record carries the checksum observed on the target, equal to the build's |
| M2-DEC-4 | Target inventory by role plus a preflight probe (M2-EN-2) | Discovery must be repeatable without committing hosts or credentials | The team maintains VMs and their snapshots | M2-O-2, -8 | Probe output in every S18 record; a seeded wrong role fails preflight |
| M2-DEC-5 | Guarded 16.8 runs inside S16 on scratch homes, before S17/S18 dispatch | Its defects take ownership of or delete sessions (impl.md sec. 7.4); the M2 rule allows no takeover or deletion on a non-scratch home before it passes | S16 needs E1 and three OSes although it is a build stage (M2-I-4) | M2-O-5 | 16.8 records and cleanup evidence before the first S17 dispatch |
| M2-DEC-6 | S17 and S18 are dispatched separately, each closing on its own report; S17 runs replay first, S18 rehearsal first and power-off last | impl.md stage sequences; a failure in one does not hide the other's evidence | Two reports for one milestone | M2-O-7, -8 | Two stage reports; M2 recorded only when both are closed |

### 4.5 Ownership, failure, retry, rollback and cleanup

Three ideas are kept apart. **Ownership** is who may change a thing and must end it
(a session's writer lease, a process tree's owner). **Access permission** is what a
caller may touch (write roots decided by the policy resolver, read access to any
session). **Desired-state matching** is whether running state follows configuration (a
preset revision, a config preview); it never grants ownership.

| Subject | Ownership rule | Failure and retry | Rollback | Cleanup, and what is retained | Proven by |
|---|---|---|---|---|---|
| Session writer (DH-D-STORE-3) | One writer per session, lease epoch checked on every append (SP-9). **Created**: the lease of the creating host. **Previously owned**: a dead holder's lease, taken over only when its liveness lock is acquired inside the write transaction, or by explicit `Force`. **Uncertain**: a live but silent holder; nothing expires by time | A second writer is refused with `session/owned` naming the holder; the log is unchanged. No automatic retry; the caller retries after the holder exits | Takeover is one transaction; a failed takeover leaves the epoch unchanged | The holder's lock file and `hosts` row are gone; a gap in `seq` keeps the home, both process logs and the integrity-check output | DH-E-STORE-1 (16.8) |
| Session deletion (DH-D-STORE-6) | Deleting removes the session's rows, blobs and spill; **shared** data (an image a fork also uses) survives; data no session references is collected | An interrupted deletion is finished by the retention duty after restart; the tombstoned session is invisible at once and never comes back | None by design: deletion is not undoable, so it runs on scratch homes only until 16.8 passes | A surviving row keeps the database with its side files | DH-E-STORE-6 (16.8) |
| Turn cut by a kill (DH-D-STORE-4, SESSION-5) | `interrupted` is written only by the session core at recovery | The cut tool call gets an "outcome unknown" result and is **never re-run** (an uncertain effect); the next request carries that result | Nothing recorded before the kill is lost; nothing is replayed | On recovery failure the database is kept | DH-E-STORE-2, DH-E-SESSION-2, DH-E-AGENT-3 |
| Process trees of commands (DH-D-EXEC-2, -4; SANDBOX-2) | **Created** trees are owned by a delegated scope, a writable cgroup, or a process group (the fallback is reported). Stop is polite, then forced, then awaited | A tree not gone at the settle deadline is an error naming survivors. If the host dies, survivors (**previously owned**, now uncertain) are listed at the next start; nothing is killed automatically. No backend, or `require: full` on a partial backend: the command does not start | None: a stop cannot be undone; the stage reports what survived | Scenario kills listed pids, removes leftover cgroup directories and checks the scope is gone; if removal fails, keeps the scope name, the runtime owner file and the host log | DH-E-EXEC-1, -10, DH-E-SANDBOX-1 |
| File writes (DH-D-FS-4) | Access, not ownership: the resolver decides the roots; concurrent edits by two processes and an editor resolve by whole writes or `fs/stale-version` | A refused call returns `fs/sandbox-denied` and leaves no staged file; a killed process releases its file lock | Staged writes are discarded, never half-applied | Outside directory compared by hash listing before and after; a changed outside file keeps both listings and the log | DH-E-FS-1, DH-E-FS-3 |
| stdio host and second launch (DH-D-SDK-3, CLI-5) | The SDK server owns stdin and stdout; a second `dsh` finds the running host instead of starting another | Client end-of-file ends the host within the bound; a crash is explained at the next start; at most ten crash reports are kept | -- | No `dsh` process remains; a leftover process's command line and the home are kept | DH-E-SDK-1, DH-E-HOST-4 |
| Presets and config (BUNDLE-4, BOOT-6) | Desired state only: a running session keeps its preset revision; `dsh config show/validate` preview and never apply | A bad layer edit leaves the running tree unchanged | -- | -- | Manual checks of S14; local tests closing H-21 |

### 4.6 Scenarios authored here that execute later

Authored, reviewed and indexed in this story, executed at the stage impl.md assigns
(rule 4 and each stage's "Scenarios" line): DH-E-SANDBOX-2, DH-E-EXEC-7, DH-E-EXEC-8
(S39, from S13); DH-E-BUNDLE-1, -2, -4 (S48) and DH-E-BUNDLE-3 (S22 by sec. 7.5) (from
S14, authored in S16); DH-E-HOST-1, -2 (S22), DH-E-HOST-7, -8, -9 and DH-E-SDK-2 (S39),
DH-E-API-5 (S48) (from S15); DH-E-CLI-1, -2 (step 27.8) and DH-E-UTIL-2 (S39) (from S16).
That is 18. DH-E-FS-5 (S28) and DH-E-CONTEXT-1 (S32) also prove M2 deliverables but need
the SSH world and the skill catalogue; they are authored when those exist (M2-Q-8).

### 4.7 The M2 enabling rule

Quoted from impl.md [sec. 3.3](../../harness/impl.md#33-milestones-and-when-a-feature-may-be-enabled):

> **Nothing is enabled for users.** Team pre-release only, on scratch homes, on Linux.
> Reasons: stopping is proven only by `DH-E-CLI-1` and `DH-E-CLI-2` (DH-S-27); no
> sandbox backend exists on macOS or Windows before DH-S-27; configuration edited by two
> processes waits for `DH-E-BOOT-2` (DH-S-21). The `acp` and `sdk-minimal` profiles are
> P1 (DH-S-37). Builds are unsigned, from the rehearsal pipeline of DH-S-1.

In plain words: at M2 the team may use `dsh headless`, `dsh sdk` and the Go client on
Linux, on scratch homes, from unsigned rehearsal builds. Several S18 scenarios also run
on macOS and Windows (fence, crash report, watching, search, private storage), because
those foundation contracts are cross-platform; passing them does not enable confined
work there, which has no backend until S27 (impl.md G-3). The guarantee covers the
tested build, inputs and environment variants only (impl.md rule 7).

---

## 5. Out of scope

| Excluded | Owner stage | Why not here |
|---|---|---|
| Web UI, routes, event stream, conversation API | DH-S-19 .. DH-S-22 (M3) | Need S16 built; M3 |
| DH-E-STORAGE-1 (two processes, workspace registry), DH-E-BOOT-2, DH-E-HUMAN-2 | 19.7, 21.13 | Need the conversation API or `dsh web` hosts |
| Stopping proven on three OSes (DH-E-CLI-1, -2) | 27.8 | Need the web UI and the macOS and Windows backends |
| macOS and Windows sandboxes, terminals, persistent shells | DH-S-27 | P1 (G-3) |
| More providers, route validation beyond one provider (H-2), images (H-3) | DH-S-23 | P1 |
| Schedules and goals (H-1, H-4); replay of sessions with children (H-10) | 30.7; 29.7 | P1 |
| `acp`, `sdk-minimal` profiles; Python SDK | DH-S-37 | P1 |
| Signed release, publication | DH-S-46 | Open owner decisions |
| Changes to `docs/harness/` or `docs/stories/harness-DSH-M1/` | -- | Requirements baseline (DC-2); inconsistencies go to sec. 11 |

---

## 6. Non-goals

M1's [NG-1 to NG-12](../harness-DSH-M1/tech-spec.md#4-non-goals) hold unchanged. Their
concrete effect in S12..S18:

| # | What it rules out in this story |
|---|---|
| NG-1 | No upstream session format or event name in the SDK protocol; scenarios use this design's fixtures |
| NG-5, NG-6 | Shell tools run programs from argument lists through one spawn path (SP-12); no embedded interpreter runs model-written code |
| NG-7 | The only outbound kind in any M2 scenario is the model request through egress; DH-E-BUNDLE-3's "send nothing but model requests" is authored here |
| NG-8 | An SDK client cannot create a session grant (unconfirmed default I2 Q5); approvals stay one-shot |
| NG-9 | No M2 tool writes a configuration layer; `dsh config` previews, `dsh secret set` is an owner command |
| NG-10 | The sandbox is reported as it is (`full` or `partial` with exposures); the harness is stated unaudited (`UP-DEBT-found.Q3.1`); M2 claims no boundary stronger than design.md sec. 6 |
| NG-11 | Stage order, placement and owner decisions are inputs; disagreements are recorded in sec. 10 and 11, not coded around |

---

## 7. Hard constraints

M1's module, gate, evidence, invariant, environment and change-control constraints
([RM-1..10, QG-1..10, EV-1..7, SP-1..14, EN-1..6, DC-1..6](../harness-DSH-M1/tech-spec.md#5-hard-constraints))
hold. Changes and additions for M2:

| ID | Constraint | Source |
|---|---|---|
| EV-6 (restated) | The real model API is never a **gate** criterion. In M2 it is reached by the DH-S-16 manual check and by scenarios in 16.8, S17 and S18, all from the credentialed workflow or a tester's shell | impl.md S16, S17; QG-10 |
| DC-1 (extended) | Commits name steps 12.1 .. 18.11 and their deliverable or scenario, and stay within one stage; S12 has one commit at which the three C-3 members are built together | impl.md S12 exit |
| M2-C-1 | Scenarios that take ownership or delete (16.8) run only on scratch homes and before any S17/S18 dispatch; the SQLite store is used on scratch homes only until they pass | impl.md sec. 7.4; M1 tech-spec sec. 3.2 |
| M2-C-2 | A missing variant, a skipped, blocked or timed-out scenario leaves its stage open; no priority makes one optional; no partial run is recorded as a pass | impl.md rules 6, 7; S-1 |
| M2-C-3 | Built and done stay separate in every record; the 24 built-not-done deliverables are listed with their finishing stage | impl.md sec. 1.3 |
| M2-C-4 | Defaults the owner has not confirmed are confirmed or changed **before the named stage starts**, and live in configuration, not in persisted formats (DC-4): S12 the full-access label (I2 Q3) and UTF-8-only text tools; S13 `partial` when delegated execution cannot be stopped (I2 Q4); S15 no SDK session grant (I2 Q5) and the client time zone (U-K2-5); S16 a second launch reuses the host (I3 Q1), Windows double-click (I3 Q8), `dsh headless` progress (U-K2-4) | impl.md [sec. 9.1](../../harness/impl.md#91-defaults-the-owner-has-not-confirmed) |
| M2-C-5 | Policy text changes are not merged until DH-E-SANDBOX-7 has passed on the new text | areas/sandbox-ssh.md DH-E-SANDBOX-7 |
| M2-C-6 | Model spend: statistical scenarios use the small model the specification names; real-model runs happen only by dispatch or schedule, never per pull request | QG-10; cost discipline |

---

## 8. Identified risks

| ID | Risk | L | I | Trigger | Mitigation | Response |
|---|---|---|---|---|---|---|
| M2-R-01 | **The model API cannot be reached** from the machines that must reach it (the authoring host already times out), blocking M1 closure, the S16 manual check, DH-E-STORE-2, all of S17 and four S18 scenarios | H | H | Preflight or a manual run fails to connect | Confirm reachability from the credentialed runners and the Linux VMs on day one (sec. 3.5); route through egress with a configured proxy if needed | Run from a network that reaches the API; never mark a scenario passed or skipped-as-pass; S17 stays open |
| M2-R-02 | **The S18 environment fleet does not exist.** About a dozen Linux variants, power-off VMs, SMB/NFS/WSL, two-account machines; hosted runners provide few of them | H | H | Preflight reports an unreachable target | Provision first (long pole); inventory and probes (M2-DEC-4); snapshots for every VM | The affected scenario stays open with the reason; M2 is not claimed; raise M2-Q-3 |
| M2-R-03 | **A confinement or fence defect** lets a model-chosen command write outside its roots, or a takeover defect puts two writers on one log | M | H | DH-E-FS-1 outside listing changes; DH-E-SANDBOX-1 finds `D`; DH-E-STORE-1 sees a gap | Fake-backend tests of every control in S13; guarded scenarios on scratch homes first; `require: full` honoured | Fix, re-run the scenario on the same variants; record the defect in the evidence record; nothing enabled meanwhile |
| M2-R-04 | **M1 does not close soon** (open S8, S9, S11; red vet gate) | M | H | First M2 commit is due and `built-through` is still DH-S-7 | M2-DEC-1; fix the inherited vet findings as their own workstream | M2 build stages wait; only environment work proceeds |
| M2-R-05 | **Time limits and statistical scenarios**: SANDBOX-7 (108 model runs), FS-2 (20 tasks), NODEFS-1 (100 power cycles per OS) exceed sec. 7.2's starting limits, or flake on thresholds | H | M | Deadline reached; a threshold missed by a small margin | Agree limits per scenario before S17/S18 (M2-Q-5); record counts beside the policy text version | A deadline is a failure; re-run only after the inputs change; never relax a threshold silently |
| M2-R-06 | **Scenarios that assume later work**: VIEW-5 and HOST-4 assert UI behaviour, LLM-3 needs a second keyed route | M | M | Then step cannot be observed | Resolve via M2-Q-6, M2-Q-7 before authoring | The scenario stays open; the owner amends the specification (DC-2) |
| M2-R-07 | **Size**: 35 deliverables, 56 steps, 32 scenario files, 37 executed scenarios over many variants | H | M | S16 not built when half the story budget is spent | Plan sub-stages (sec. 3.2); S14 beside S12/S13; environment work in parallel | Stop at the last built stage and re-plan; never mark a partial stage built |
| M2-R-08 | **Key leakage** in scenario logs, crash reports or reports (carries M1 R-11) | L | H | Key substring in an artifact | Key only in the child's environment; redaction tests; review reports before linking | Rotate the key, purge the artifact, add a regression test |
| M2-R-09 | **Windows and macOS semantics** in 16.8 and S18 (file locking, replace, private directories, crash reports) | M | M | A scenario passes on Linux and fails elsewhere | OS-specific test files and three-OS gate from DH-S-1 | Fix per OS; a platform limit gets a register entry, not a skip |
| M2-R-10 | **Real-model cost** of S17/S18 and repeated runs | M | M | Spend exceeds an agreed budget | Small model; replay first; no per-PR runs (M2-C-6) | Pause dispatches and ask the owner; never substitute replay for a scenario |

---

## 9. Assumptions and dependencies

| ID | Assumption | If false |
|---|---|---|
| M2-A-1 | An operator can supply a DeepSeek key at run time and the credentialed runners reach the API | M2-R-01 response |
| M2-A-2 | The team can keep the VMs of M2-EN-1 with snapshots, reachable over SSH from a controller | S18 cannot close (M2-R-02) |
| M2-A-3 | GitHub-hosted Linux, macOS and Windows runners remain available | 16.8 and parts of S18 move to team VMs |
| M2-A-4 | The ledger's `proof` lists are current, so sec. 3.3's done-at-M2 column holds | The plan re-derives it at each stage entry |

External dependencies: bubblewrap and Landlock-capable kernels; `systemd --user` with
cgroup v2; Git; real editors (Vim, VS Code, Notepad); `rg` for comparison only; public
repositories for FS-2 and RIPGREP; a hypervisor that can power off a VM; SMB and NFS
servers; GitHub Actions; the DeepSeek Messages endpoint.

---

## 10. Open questions

| ID | Question | Needed before |
|---|---|---|
| M2-Q-1 | **Story estimate.** No point value is given here. Size facts: 7 stages, 56 steps, 35 deliverables, 32 scenario files, 37 scenarios executed across about a dozen environment variants, plus environment provisioning. Does the owner size it as one story or split acceptance (S17, S18) from build (S12..S16)? | Plan approval |
| M2-Q-2 | Impl.md G-3: may a first working harness be Linux-only for confined work, or are SANDBOX-5 and SANDBOX-6 raised? | S13 start |
| M2-Q-3 | Who provides and pays for the VM fleet of M2-EN-1, and the SMB/NFS/WSL and power-off setups? | Story start |
| M2-Q-4 | Confirm or change the unconfirmed defaults of M2-C-4 | Each named stage |
| M2-Q-5 | Per-scenario deadlines for SANDBOX-7, FS-2 and NODEFS-1, and the model budget for S17/S18 | S17/S18 dispatch |
| M2-Q-6 | DH-E-VIEW-5 step (5) and DH-E-HOST-4 assert UI behaviour (directory picker; "reaches the UI") before the UI exists: is the listener's response enough at S18, or do these assertions move? | Authoring VIEW-5; S18 |
| M2-Q-7 | DH-E-LLM-3 needs "a second working keyed route": is a second `deepseek-official` route with a valid key acceptable before S23? | S17 |
| M2-Q-8 | DH-E-FS-5 and DH-E-CONTEXT-1 prove M2 deliverables but need later work: authored in their executing stages? | M2 close |
| M2-Q-9 | DH-E-SANDBOX-2 is Linux-only by its specification: move it to S18 (M2-I-3)? | S13 authoring |

---

## 11. Inconsistencies found

Recorded per DC-3; nothing under `docs/harness/` or `docs/stories/harness-DSH-M1/` is
changed by this story.

| ID | Inconsistency | Citations | Choice taken in this spec |
|---|---|---|---|
| M2-I-1 | Fact F-7 says `DH-E-EXEC-7` runs in DH-S-18; DH-S-13 and sec. 7.5 place it in DH-S-39 | impl.md sec. 9.3 F-7; DH-S-13 "Scenarios"; sec. 7.5 | sec. 7.5 governs: S39 |
| M2-I-2 | DH-S-14 says `DH-E-BUNDLE-3` runs in DH-S-39; sec. 7.5 indexes it to DH-S-22 | impl.md DH-S-14; sec. 7.5 | sec. 7.5 governs: S22 |
| M2-I-3 | `DH-E-SANDBOX-2` "needs only the Linux backends of the same stage", but is placed in DH-S-39 with the every-backend scenarios; rule 8 would place it in S18 | areas/sandbox-ssh.md DH-E-SANDBOX-2; impl.md DH-S-13 | S39 (index governs); M2-Q-9 |
| M2-I-4 | E1's stage list omits 16 and 18, and DH-S-18's Entry names only E2, yet DH-E-STORE-2 (16.8), DH-E-FS-2, DH-E-VIEW-5 and DH-E-CONTEXT-2 (S18) need a real model | impl.md sec. 7.1; DH-S-18 Entry; scenario Givens | E1 is required in S16 and S18 |
| M2-I-5 | Step 16.8 runs "on Linux, macOS and Windows", but DH-E-STORE-2 needs "a real shell under the platform's sandbox", which exists only on Linux before S27 | impl.md step 16.8; areas/session-store.md DH-E-STORE-2 | STORE-2 on Linux only at 16.8 |
| M2-I-6 | DH-E-VIEW-5 and DH-E-HOST-4 assert UI behaviour, though they run in S18, before the UI stages | areas/session-views.md; areas/host-telemetry.md | M2-Q-6 |
| M2-I-7 | DH-S-17 Entry says "with its guarded scenario passed" (singular); step 16.8 has four | impl.md DH-S-17 | All four |
| M2-I-8 | The stage index marks only DH-S-18 "(M2)"; M2 needs S17 and S18 closed and 16.8 passed | impl.md sec. 5; sec. 3.3 | sec. 3.3 governs |
| M2-I-9 | DH-S-17 says it pairs with DH-S-14, but no S14 scenario runs in S17 | impl.md DH-S-17, DH-S-14 | S14 deliverables are built at M2, done at S22/S48 |
| M2-I-10 | `DH-E-EXEC-10` and `DH-E-STORAGE-6` prove S6 deliverables and are indexed to S18 (S6's paired stage), so rule 4 required S6 to author them; the index lacks both, while the DH-S-11 evidence says every S1..S11 scenario is authored | impl.md rule 4; acceptance-index.yaml; DH-S-11.md | Authored in this story (sec. 4.2) |
| M2-I-11 | sec. 7.2 names the key `DEEPSEEK_API_KEY` ("for example") and a built `DSH_BIN`; the workflow uses `DSH_DEEPSEEK_API_KEY` and runs scenarios from source on three fixed OSes | impl.md sec. 7.2; harness-scenarios.yml | Keep the secret name; M2-DEC-2, -3 |
| M2-I-12 | sec. 7.2's per-scenario limits (15 and 20 minutes) cannot hold SANDBOX-7's 108 model runs or NODEFS-1's 100 power cycles per OS | impl.md sec. 7.2; scenario Whens | M2-Q-5 |
| M2-I-13 | The sec. 3.1 diagram chains S13 -> S14 -> S15, while S14 depends only on S6, S11 and may run beside S12/S13 (sec. 5) | impl.md sec. 3.1, sec. 5 | Entry lines govern (sec. 3.5) |

---

## 12. Coverage of the brief

| Element | Where handled |
|---|---|
| Stages DH-S-12 to DH-S-18, result, entry, priority, steps, exit evidence, split flags | sec. 3.2 |
| M1 closure as entry prerequisite | sec. 1.5, 3.1 (M2-DEC-1), M2-DOD-1 |
| A-B-C links, no orphans | sec. 2 (A) -> sec. 3.3 (B) -> sec. 4.2, 4.6 (C) |
| Real-environment acceptance: targets, discovery, credentials, workflow and preflight, timeouts, reports, recovery, cleanup | sec. 4.3, 4.4 |
| Local versus real-environment evidence; open never passes | sec. 4.1, M2-C-2 |
| Enabling rule, exact | sec. 4.7 |
| Ownership, failure, retry, rollback, cleanup | sec. 4.5 |
| Held-open items (H-1..H-4, H-10, H-21) | sec. 3.3, M2-DOD-5 |
| Status separation; built versus done | sec. 3.6, M2-C-3 |
| Size facts and estimate | sec. 3.2, M2-Q-1 |
| Inconsistencies in impl.md | sec. 11 |
