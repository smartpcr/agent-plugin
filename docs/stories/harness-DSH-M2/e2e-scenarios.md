---
title: "dsh M2 -- first prompt from the command line and the SDK -- end-to-end scenarios"
storyId: "harness:DSH-M2"
---

## About this document

Story `harness:DSH-M2`, operator prompt: "clone deepseek harness, given context in
docs\harness of this repo, and design and impl doc, i want you to follow impl. This
story is milestone M2 of dsh (impl.md stages DH-S-12 to DH-S-18), following
harness:DSH-M1." The milestone is reached when `dsh headless "<task>"` answers a prompt
in a terminal, `dsh sdk` serves a Go program through `src/sdk/sdk`, both against a real
model through the user's signed-in client, with file and shell tools running inside
the fence and the Linux sandbox -- and when that is **proved** on real machines
([tech-spec.md](tech-spec.md) sec. 1.1, sec. 3.5).

This file holds the Gherkin scenarios QA runs to accept each phase. It owns the
**scripts and run schedule** of the 37 `DH-E-*` scenarios the story executes
(tech-spec sec. 4.2) and the authoring checks of the 17 it only writes (tech-spec sec.
4.5). It does not restate:

| Owned elsewhere | Where |
|---|---|
| Components C31..C57, flows F1..F7, invariants INV-1..INV-14, proof rows TC-01a..TC-21s5, `service:*` names | [architecture.md](architecture.md), "Test & Environment Contract" |
| Scope, constraints M2-C-1..14, decisions M2-DEC-1..6, environments M2-EN-1..8, risks, open questions M2-Q-1..7 | [tech-spec.md](tech-spec.md) |
| Steps, commits, per-stage gate tests, evidence-record and closure work | [implementation-plan.md](implementation-plan.md) |
| Each `DH-E-*` Given/When/Then/Cleanup as written by its area or library spec | `docs/harness/areas/*.md`, `docs/harness/libs/*.md`, `docs/harness/upstream/toolchain.md` |

Rule 6 of architecture.md's contract binds this file: a `DH-E-*` script **keeps its
specification's Given, When, Then and Cleanup** and adds only the environment binding
(machine, `service:*`, env-var inputs, route, platform). Where a binding changes a
Given, the change is one of the amendments already decided (signed-in route for "a real
model API key", M2-I-1; two platforms, M2-EN-2) and is marked `[binding]`.

### Phases mirror the implementation plan

| Phase here | implementation-plan.md | impl.md | Venue | Setup type |
|---|---|---|---|---|
| 1 Entry and Evidence Scaffolding | Phase 1 | M2-DEC-1, sec. 7.3 | gate | inline |
| 2 Working Directory, Filesystem Seam and Policy Resolver | Phase 2 | DH-S-12 | gate | inline |
| 3 Sandbox Launch, Linux Backends and Shell Tools | Phase 3 | DH-S-13 | gate | inline |
| 4 Agent Presets and Bundles | Phase 4 | DH-S-14 | gate | inline |
| 5 Listener, Sign-In, Stdio and SDK Server | Phase 5 | DH-S-15 | gate | inline |
| 6 Profiles, Launchers and Evidence Runners | Phase 6 (Stages 6.1-6.11) | DH-S-16, steps 16.1-16.7, 16.9, 16.10 | gate | inline |
| 7 Guarded Scenarios on the Lane Machines | Phase 6 (Stage 6.12; all four 16.8 runs) | DH-S-16, step 16.8 | guarded | lab-bare-metal |
| 8 Acceptance on a Real Model | Phase 7 | DH-S-17 | acceptance | lab-bare-metal |
| 9 Acceptance on Real Operating Systems | Phase 8 | DH-S-18 | acceptance | lab-bare-metal |
| 10 M2 Milestone Closure | Phase 9 | sec. 3.3 M2 row | gate | inline |

Implementation-plan Phase 6 is split across Phases 6 and 7 here because its Stage 6.12
is a different venue with a different Setup. Stages 6.1-6.11 are gate work that
self-provisions (`inline`). Stage 6.12 runs the four step 16.8 scenarios in one
`guarded` invocation of each lane script, on the lane machines, with
`DSH_REQUIRE_SCENARIOS` taken from `harness-gate require --stage DH-S-16`. Two of the
four depend on provided services that no test can create:
- DH-E-FS-3 needs `service:git-and-editor`, an editor save script installed on each lane
  machine (TC-19s1; plan Stage 6.12).
- DH-E-STORE-2 needs the tester's signed-in Claude Code client and `bwrap` Full
  (TC-19s2).

So Phase 7 is `lab-bare-metal`. Phase 7 finishes before any Phase 8 or 9 run (contract
rule 7), and S16 closes only when all four have passed.

### Three kinds of scenario -- never mixed

| Kind | Tag | What it is | Where it runs | Closes |
|---|---|---|---|---|
| **Gate** | `@venue:gate` | Go unit, contract, golden, re-exec, loopback and built-binary tests proving one TC row | Both gate lanes (`tools/harness-gate/lanes/win.ps1`, `lanes/wsl.sh`) and the hosted `harness` job of `.github/workflows/ci.yml` (`ubuntu-latest`, `windows-latest`) | The *built* state of a step |
| **Guarded** | `@venue:guarded` | The four step 16.8 `DH-E-*` runs, `-tags scenario`, on scratch homes, before S16 closes | The lane scripts' `--guarded` mode (implementation-plan Stage 6.12) | S16 closure, entry to S17/S18 |
| **Acceptance** | `@venue:acceptance` | The 14 S17 and 19 S18 `DH-E-*` runs on real model routes and real operating systems | Labelled self-hosted runners, team-kept machines and runbooks (Phases 8, 9) | The *done* state of the deliverables they finish, through an evidence record only (M2-DEC-5) |

A guarded or acceptance scenario never appears in a gate required list, and a gate
scenario never depends on a provided service (contract rules 2, 3, 8).

### Conventions

- **Tags.** `@TC-xx` the architecture proof row; `@proof:in-process`,
  `@proof:golden`, `@proof:service:<name>` the strategy; `@DH-E-*`, `@DH-T-*`,
  `@DH-D-*` the impl.md ids; `@venue:*` above; `@route:claude-code` (required leg) and
  `@route:copilot-cli` (recorded leg, M2-DEC-4); `@platform:linux-wsl`,
  `@platform:windows`, `@platform:linux-e2:<variant>`; `@edge` an edge or failure case;
  `@manual` a runbook step a person performs and records.
- **Result vocabulary.** A scenario row in an evidence record reads exactly one of
  `passed`, `failed`, `not run (<reason>)`, `out of scope (<decision>)`. A skipped
  test is `failed` (INV-13, M2-DEC-3). *Not run* never counts as *passed*, and an
  in-scope variant that is *not run* keeps its stage open (M2-C-2, M2-DOD-4). macOS rows
  read `out of scope (platforms-windows-wsl)` (M2-EN-2, Q `m2-macos-rows`).
- **Scratch homes only (M2-C-1, M2-EN-6).** Every scenario that starts `dsh` gets a
  fresh home from `t.TempDir()` through `testkit/scenario` (C55) or the M1
  `testkit/launcher`; `DSH_HOME` is removed from the child environment, so a developer's
  real home is never opened. Workspaces are scratch directories too.
- **No key, anywhere (M2-NG-2, M2-C-11).** Real-model scenarios reach a model only
  through the signed-in Claude Code CLI (`service:model-route-claude-code`, must pass)
  or Copilot CLI (`service:model-route-copilot`, recorded). The test kit's launcher
  strips every credential-shaped variable from dsh's environment. A signed-out leg
  points `CLAUDE_CONFIG_DIR` or `COPILOT_HOME` at an empty temporary directory.
- **Env-var names, never literals.** Every machine, share, address or template is an
  env-var input (table below). No hostname, IP, share path, account password or token
  appears in this file, in a scenario file or in a workflow.
- **Virtual time in the gate.** Gate timing scenarios use `testing/synctest` or the
  test kit's virtual clock (analyzer `nosleep`). Guarded and acceptance scenarios use
  the wall-clock bounds their specifications state (for example STORE-1's 60 s per
  step) and impl.md sec. 7.2's per-scenario limits.
- **Built binary once.**
  - Gate scenarios build `dsh` once per test binary: through the M1
    `testkit/launcher.Build` in Phases 3-5, and through `testkit/dshbin` (C54, a wrapper
    over it) from Phase 6.
  - Guarded and acceptance scenarios receive it through `DSH_BIN`, pointing at the
    binary the lane script built once, whose checksum the evidence record cites
    (F7 "build once").

### Env-var inputs

"Existing" means the name is already read by a file under
`src/harness/internal/testkit/scenarios/`; "new" names are introduced by the scenario
files this story authors and follow the same `need(t, ...)` pattern (a missing input
fails the scenario naming the variable; it never skips).

| Env var | Kind | Used by | When unset |
|---|---|---|---|
| `DSH_BIN` | path to built `dsh` | C53, C55, every guarded and acceptance scenario | Gate: Phases 5 and earlier build with the M1 `testkit/launcher.Build`; Phase 6 onward with `testkit/dshbin` (C54, a wrapper over it) into `t.TempDir()`. Guarded and acceptance: set by the lane script to the binary it built once; unset fails the run |
| `DSH_REQUIRE_SCENARIOS` | comma list | C55, `harness-scenarios.yml` preflight | Gate: empty, nothing required. Guarded/acceptance: derived by `harness-gate require --stage <id>` |
| `DSH_SCENARIO_REPORT` | file path | C55 evidence statement; NODEFS-1 version log | Scenario writes its statement to `t.TempDir()` and logs the path |
| `DSH_HOME` | path | dsh by hand only | Always removed from children by the test kit |
| `DSH_SCENARIO_CLAUDE_CODE_MODEL`, `DSH_SCENARIO_CLAUDE_CODE_EFFORT` | route inputs (existing, `signedin_routes_test.go`) | Phases 7, 8, FS-2, VIEW-5 | Required on lab phases; run fails |
| `DSH_SCENARIO_COPILOT_CLI_MODEL`, `DSH_SCENARIO_COPILOT_CLI_EFFORT` | route inputs (existing) | Phase 8 recorded legs | Recorded leg reads `failed (input missing)`; the required leg is unaffected |
| `CLAUDE_CONFIG_DIR`, `COPILOT_HOME` | client config homes | LLM-3, VIEW-1 signed-out steps | Set by the scenario to an empty temp dir only for the signed-out step; otherwise inherited from the signed-in user |
| `DSH_SCENARIO_STORE1_*`, `DSH_SCENARIO_STORE6_*` | command templates (existing) | Phase 7 guarded | Exported by the lane script's guarded mode; unset fails the scenario naming the variable |
| `DSH_SCENARIO_FS3_EDITOR` | command template over `{file}` (new) | DH-E-FS-3 | Required: names the editor save script installed on the lane machine (`service:git-and-editor`); unset fails preflight |
| `DSH_SCENARIO_STORE2_START`, `_PROMPT`, `_RESUME`, `_REQUEST`, `_KILL` | command templates (existing; `_KEY` removed, M2-I-1) | DH-E-STORE-2 | Required; run fails |
| `DSH_SCENARIO_SDK1_LAYER`, `DSH_SCENARIO_SDK1_LONG_PROMPT` | layer text, prompt (new) | DH-E-SDK-1 | Required |
| `DSH_SCENARIO_SANDBOX7_TASKS`, `DSH_SCENARIO_SANDBOX7_PREVIOUS_SHARE` | task-set dir, previous escalation share (new) | DH-E-SANDBOX-7 | Task set: the committed `testdata/sandbox7/`; previous share: 10/18 floor |
| `DSH_SCENARIO_SANDBOX1_MACHINES` | `;`-separated SSH aliases (new) | DH-E-SANDBOX-1, NATIVESYSTEM-1..4 | Required |
| `DSH_SCENARIO_EXEC1_*`, `DSH_SCENARIO_EXEC10_SSH`, `DSH_SCENARIO_EXEC10_SSH_NOMANAGER`, `DSH_SCENARIO_EXEC10_*` | templates, SSH aliases (existing) | EXEC-1, EXEC-10 | Required |
| `DSH_SCENARIO_HOST4_LAYER`, `_ARGS`, `_READY` | layer, args, readiness probe over `{home}` and `{bin}` (existing; `{bin}` added by Stage 8.3) | DH-E-HOST-4 | Required; the layer only by the load, panic, fault and twelve-failure legs |
| `DSH_SCENARIO_STORAGE6_USER`, `_PUBLIC`, `_SEED`, `_LIST`, `_READ`, `_START`, `_STATUS`, `_STOP` | account name, templates (existing) | DH-E-STORAGE-6 | Required |
| `DSH_SCENARIO_SEARCH_BUDGET` | Go duration (existing) | DH-E-RIPGREP-1 | Required; the agreed budget is recorded with the result |
| `DSH_SCENARIO_FS_CHECKOUTS`, `DSH_SCENARIO_FS1_SHARE`, `DSH_SCENARIO_WATCH_SHARES` | checkout dirs, SMB/NFS mount points (new) | FS-1, FS-2, RIPGREP-*, CHOKIDAR-2 | Required on the variant that names them |
| `DSH_SCENARIO_VMS`, `DSH_SCENARIO_NODEFS_*` | VM names, controller templates (overlay create/delete, power off/on, guest-agent read), ack listener (existing) | DH-E-NODEFS-1 | Required |
| `DSH_LAB_EVIDENCE_URL` | artifact container URL (new) | Phases 7-9 Cleanup clauses that keep a home or database | Artifacts stay on the machine; the record names the local path |

### Gate provisioning in one table

All six `inline` phases (1-6, 10) run their required scenarios with **no docker, no
service, no network and no credential**; every dependency is one of architecture.md's
gate-local services and is created by the test itself. No phase here is `compose`: the
only persisted service, `service:sqlite`, is embedded `modernc.org/sqlite` in the test
binary with its file under a `t.TempDir()` home, so an env-var-selected external store
has nothing to add (unlike M1's `DSH_E2E_STORE_HOME` lanes under `tests/e2e/`, which
this story does not extend).

| Gate-local service | Self-provisioned by | Phases |
|---|---|---|
| `service:filesystem` | `t.TempDir()`; symlinks by the test (Windows: junction where a symlink is refused, recorded) | 2, 3, 5, 6 |
| `service:os-process` | Test binary re-exec (helper pattern) through `world/subprocess.Start`, or built `dsh` from `testkit/launcher.Build` (Phases 3-5) / `testkit/dshbin` (Phase 6); reaped by `testkit/procwait` | 3, 5, 6 |
| `service:go-toolchain` | The lane's Go 1.27.4 | all |
| `service:loopback-http` | `127.0.0.1:0`, per-test certificates | 5 |
| `service:sqlite` | Embedded modernc SQLite in a temp home | 2, 4, 5, 6 |
| `service:local-socket` | Unix socket in `t.TempDir()`; AF_UNIX on Windows | 5, 6 |
| Shells | `bash` on the Linux lane, `pwsh` on the Windows lane; preflight **fails** when missing (Stage 6.10) | 3, 6 |

The three `lab-bare-metal` phases (7-9) depend on provided services
(`service:model-route-*`, `service:linux-*`, `service:windows-host`,
`service:git-and-editor`, `service:vm-power-cut`, `service:ack-recorder`). Each has a
preflight that **fails** the run when its service is absent; the evidence row then
reads `not run (<service> preflight failed)` and the stage stays open. That is the only
form a "skip" takes in this story, and it is never counted as passed.

### Scenario schedule (37 executed, 17 authored)

| `DH-E-*` | Phase (venue) | Platforms in scope | TC row | Scenario file |
|---|---|---|---|---|
| STORE-1, STORE-6 | 7 (guarded) | linux-wsl, windows | TC-19s1 | `store_two_process_scenario_test.go` (`TestTwoProcessesShareOneHomeWithOneWriter`), `store_deletion_scenario_test.go` (`TestDeletedSessionLeavesNothingBehind`) |
| FS-3 | 7 (guarded) | linux-wsl, windows | TC-19s1 | `fs_two_process_scenario_test.go` (new; `TestTwoProcessesAndAnEditorLoseNoEdit`) |
| STORE-2 | 7 (guarded) | linux-wsl only (impl.md 16.8) | TC-19s2 | `store_crash_recovery_scenario_test.go` (`TestKilledHostRecoversHonestly`) |
| TOOLCHAIN-1 (first), SESSION-1, SESSION-2, AGENT-1..4, LLM-1, LLM-3, VIEW-1, TOOLS-1, LOADER-1, SDK-1, SANDBOX-7 (last) | 8 (acceptance) | Linux E1 | TC-20s | existing files; `sdk_stdio_scenario_test.go`, `sandbox_policy_text_scenario_test.go` (new) |
| TOOLCHAIN-2 (first) | 9 | linux, windows | TC-21s3 | `toolchain_rehearsal_scenario_test.go` |
| HOST-4 | 9 | linux, windows | TC-21s3 | `host_report_scenario_test.go` |
| EXEC-1, EXEC-10 | 9 | Linux E2 (systemd/cgroup VMs) | TC-21s2 | `exec_tree_scenario_test.go`, `exec_scope_ssh_scenario_test.go` |
| SANDBOX-1, NATIVESYSTEM-1..4 | 9 | Linux E2 machines: bwrap, Landlock only (ABI >= 5), unconfined (neither backend); plus NATIVESYSTEM variants Landlock ABI 1-4 and musl | TC-21s1, TC-04s | `sandbox_status_scenario_test.go`, `sandbox_native_scenario_test.go` (new) |
| FS-1 | 9 | Linux ext4, Windows NTFS, SMB | TC-21s3 | `fs_fence_scenario_test.go` (new) |
| FS-2, VIEW-5 | 9 | FS-2: linux, windows; VIEW-5: linux | TC-21s4 | `fs_long_scenario_test.go`, `view_workdir_scenario_test.go` (new) |
| RIPGREP-1, RIPGREP-2, CHOKIDAR-1, CHOKIDAR-2 | 9 | linux, windows (+ SMB/NFS, WSL-to-Windows for CHOKIDAR-2) | TC-21s3 | `search_scenario_test.go`, `watch_scenario_test.go` |
| CONTEXT-2 | 9 | linux, windows | TC-07s, TC-21s4 | `context_instructions_scenario_test.go` (new) |
| STORAGE-6 | 9 | linux, windows (two accounts) | TC-21s3 | `storage_private_scenario_test.go` |
| NODEFS-1 (last) | 9 | Linux guest (KVM), Windows guest (Hyper-V) | TC-21s5 | `durable_replace_scenario_test.go` |
| Authored only: SANDBOX-2, EXEC-7, EXEC-8 | 3 | -- (run at DH-S-39) | -- | compile + index check |
| Authored only: HOST-1, HOST-2 (DH-S-22); HOST-7, HOST-8, HOST-9 (DH-S-39) | 5 | -- | -- | compile + index check |
| Authored only: SDK-2, UTIL-2 (DH-S-39); CLI-1, CLI-2 (step 27.8); BUNDLE-3 (DH-S-22); BUNDLE-1, BUNDLE-2, BUNDLE-4, API-5 (DH-S-48) | 6 | -- | -- | compile + index check |

---

# Phase 1: Entry and Evidence Scaffolding

Implementation-plan Phase 1. C57 acceptance plumbing: the evidence records
`docs/stories/harness-DSH-M2/evidence/DH-S-12.md` .. `DH-S-18.md` and `closure.md`, the
two-step stage-closure protocol (`harness-gate ready`, then `harness-gate close`) and the
M1 entry check (`harness-gate entry`, M2-DEC-1). Everything later that says "the stage
closes" goes through the predicates proven here.

### Setup
- **Type**: inline
- **Local**: from the repository root with Go 1.27.4:
  `GOWORK=off go -C tools/harness-gate test -race -shuffle=on ./...`; then the live
  entry check `GOWORK=off go -C tools/harness-gate run . entry --story harness:DSH-M2 --root ../..`.
  On Windows run `tools/harness-gate/lanes/win.ps1`; on Linux under WSL run
  `tools/harness-gate/lanes/wsl.sh` (both run the same gate).
- **CI runner**: GitHub-hosted `ubuntu-latest` and `windows-latest`, job `harness` of
  `.github/workflows/ci.yml`. No self-hosted runner, no ADO pool.
- **Secrets**: none.
- **Pre-test bootstrap**: none.
- **Gate provisioning**: no connection env-var exists. Evidence records and indexes are
  string fixtures (in-process) or the committed immutable trees
  `tools/harness-gate/testdata/closure/{open_not_ready,open_ready,closed_ready,closed_not_ready}/`
  and `testdata/entry/{m1_open,m1_closed}/` copied into `t.TempDir()` and passed with
  `--root`. The live-record scenario reads the committed repository files read-only.
  Nothing is downloaded; no scenario skips.

### Scenarios

#### Feature: A stage closes only through ready, then closed

- **Scenario:** Ready ignores the status line and checks every row
  ```gherkin
  @TC-17a @proof:in-process @venue:gate
  Given an index listing DH-E-STORE-1 under DH-S-16
  And an evidence record reading "Closure status: open" with the DH-E-STORE-1 row "passed"
  When "harness-gate ready --stage DH-S-16" evaluates it
  Then it exits 0 with no blocker
  And with the row changed to "not run (bwrap preflight failed)" it exits non-zero naming DH-E-STORE-1 and "not run"
  ```
- **Scenario:** Skipped is never passed
  ```gherkin
  @TC-17a @proof:in-process @venue:gate @edge
  Given an evidence record whose DH-E-FS-3 row reads "skipped"
  When the ready predicate runs for DH-S-16
  Then the stage is refused with a message containing "skipped is not passed"
  ```
- **Scenario:** Close requires ready plus the transition
  ```gherkin
  @TC-17a @proof:golden @venue:gate
  Given the four committed fixture trees under tools/harness-gate/testdata/closure/
  When "harness-gate close --stage DH-S-16 --root <tree>" runs on each
  Then only closed_ready exits 0
  And open_ready is refused "not-yet-closed"
  And closed_not_ready is refused "closed-but-not-ready"
  And the combined output equals testdata/closure/expected.golden
  ```
- **Scenario:** Out of scope by decision is not the same as not run
  ```gherkin
  @TC-17a @proof:in-process @venue:gate @edge
  Given a DH-S-18 record whose DH-E-STORAGE-6 macOS row reads "out of scope (platforms-windows-wsl)"
  And whose Windows and Linux rows read "passed"
  When the ready predicate runs
  Then the macOS row is not a blocker
  And replacing "out of scope (platforms-windows-wsl)" with "out of scope" (no decision named) makes it a blocker
  ```
- **Scenario:** Built-mode ready requires a commit per step and every owner default
  ```gherkin
  @TC-17a @proof:in-process @venue:gate @edge
  Given an in-memory DH-S-12 record with every step SHA present and the "Owner defaults (M2-C-4)" table row "UTF-8-only text tools" empty
  When "harness-gate ready --stage DH-S-12 --built" evaluates it
  Then it exits non-zero naming "UTF-8-only text tools" and M2-C-4
  ```

#### Feature: No M2 run before M1 is closed

- **Scenario:** Entry refused while any M1 record is open
  ```gherkin
  @proof:in-process @venue:gate
  Given eleven in-memory M1 records of which DH-S-11 reads "Closure status: open"
  When the entry predicate runs for harness:DSH-M2
  Then it fails naming DH-S-11
  And with all eleven closed, an M1 closure record and "built-through: DH-S-11" it passes
  ```
- **Scenario:** Entry over immutable fixtures
  ```gherkin
  @proof:golden @venue:gate
  Given the committed trees testdata/entry/m1_open and testdata/entry/m1_closed
  When "harness-gate entry --story harness:DSH-M2 --root <tree>" runs on each
  Then m1_open exits non-zero with output equal to testdata/entry/m1_open.golden
  And m1_closed exits 0
  ```
- **Scenario:** The live entry result agrees with the live records
  ```gherkin
  @proof:golden @venue:gate
  Given the committed docs/stories/harness-DSH-M1/evidence/ and test/harness/acceptance-index.yaml
  When "harness-gate entry" runs against the repository root
  Then it exits 0 if and only if all eleven records read closed, the closure record exists and built-through is at least DH-S-11
  And otherwise its output names exactly the unmet conditions
  ```

#### Feature: Records and index skeleton exist before the first build step

- **Scenario:** A newly created stage record starts open
  ```gherkin
  @proof:golden @venue:gate
  Given the immutable creation fixture tools/harness-gate/testdata/records/created/ (one freshly scaffolded record per stage DH-S-12 .. DH-S-18 and one closure.md), never the live evidence directory
  When the record-shape check runs with --root pointed at that fixture
  Then each stage record holds the impl.md sec. 7.3 fields and reads "Closure status: open"
  And closure.md has 35 deliverable rows and rows for H-1, H-2, H-3, H-4, H-10 and H-21
  ```
- **Scenario:** Live records stay valid whether open or closed
  ```gherkin
  @proof:golden @venue:gate
  Given the committed docs/stories/harness-DSH-M2/evidence/
  When the record-shape check runs against the repository root
  Then DH-S-12.md .. DH-S-18.md each exist and hold the impl.md sec. 7.3 fields
  And each status line reads either "Closure status: open" or "Closure status: closed"; any other value fails naming the record
  And a record reading closed must also satisfy "harness-gate close --stage <id>", so closing DH-S-12 legitimately keeps this check green
  ```
- **Scenario:** Index stage keys added without moving built-through
  ```gherkin
  @proof:golden @venue:gate
  Given the committed acceptance-index.yaml
  When the index check runs
  Then keys DH-S-16, DH-S-17, DH-S-18, DH-S-22, DH-S-27, DH-S-39 and DH-S-48 exist
  And built-through still reads DH-S-8 until M1 closure advances it
  ```

---

# Phase 2: Working Directory, Filesystem Seam and Policy Resolver

impl.md DH-S-12, implementation-plan Phase 2. Components C31 (`session/workdir`), C32
(`world/fs`, `fs/local`, `fs/ops`), C33 (`world/sandbox/policy`), C34
(`human/permission`), C35 (`world/fs/tools`). Cycle C-3 lands as one unit. Proves
TC-01a, TC-01b, TC-01g, TC-02a, TC-03b. Authors DH-E-FS-1, FS-2, FS-3, VIEW-5 and
SANDBOX-7 (run in Phases 6, 8, 9).

### Setup
- **Type**: inline
- **Local**: `go -C src/harness test -race -shuffle=on ./internal/session/workdir/... ./internal/world/fs/... ./internal/world/sandbox/policy/... ./internal/human/permission/...`
  on both lanes (`lanes/win.ps1`, `lanes/wsl.sh`); the authoring check is
  `go -C src/harness vet -tags scenario ./internal/testkit/scenarios/...`.
- **CI runner**: GitHub-hosted `ubuntu-latest` and `windows-latest`, `harness` job of
  `ci.yml`.
- **Secrets**: none.
- **Pre-test bootstrap**: none. On the Windows lane, symlink scenarios use Developer
  Mode when present and fall back to a junction otherwise; the test records which.
- **Gate provisioning**: no connection env-var. Logic rows run on `world/fs/fstest`'s
  in-memory seam, a fake clock and in-memory views; filesystem rows use `t.TempDir()`;
  the resume row uses embedded modernc SQLite in `t.TempDir()`. The link-swap helper is
  a goroutine in the test. No scenario skips on either lane.

### Scenarios

#### Feature: Cycle C-3 contracts (C31, C32, C33)

- **Scenario:** Policy has no valid zero value
  ```gherkin
  @TC-01a @proof:in-process @venue:gate @DH-D-SANDBOX-1 @edge
  Given "var p sandbox.Policy" with no resolver call
  When the fake seam's write check receives p
  Then it returns an herr error with code "sandbox/unavailable"
  And it neither allows nor silently denies the write
  ```
- **Scenario:** The fake seam passes its own conformance suite
  ```gherkin
  @TC-01a @proof:in-process @venue:gate
  Given fstest.NewMemFS()
  When fstest.Run executes
  Then every subtest passes, including fence refusal with "fs/sandbox-denied" and a stale write refused "fs/stale-version"
  ```
- **Scenario:** Only fs/ops imports the resolver
  ```gherkin
  @TC-01a @proof:service:go-toolchain @venue:gate @edge
  Given a seeded archtest fixture where session/workdir imports world/fs/local
  When the import rule runs
  Then it fails naming that import
  And the real tree passes, with fs/ops the sole importer of world/sandbox/policy (INV-1)
  ```

#### Feature: One working directory per session (VIEW-10, C31)

- **Scenario:** The fold is rebuilt from the log
  ```gherkin
  @TC-01a @proof:in-process @venue:gate @DH-D-VIEW-10
  Given an in-memory log with two workingDirectory changes A then B
  When the views are rebuilt
  Then workdir.Access.Path() is B and the fold's workspace and root match B
  ```
- **Scenario:** Concurrent changes serialize
  ```gherkin
  @TC-01a @proof:in-process @venue:gate @edge
  Given two goroutines calling Change on one session inside a synctest bubble
  When both return
  Then the log holds two ordered events and the fold equals the later one
  ```
- **Scenario:** A deleted directory is reported once
  ```gherkin
  @TC-01b @proof:service:filesystem @venue:gate @edge
  Given a session whose working directory is a t.TempDir() subdirectory the test then removes
  When the next tool call asks workdir.Access
  Then it returns "workdir/unavailable"
  And exactly one inbox notice is queued
  ```

#### Feature: The fence (FS-4, C32)

- **Scenario:** Conformance on the real provider, both lanes
  ```gherkin
  @TC-01b @proof:service:filesystem @venue:gate @platform:linux-wsl @platform:windows
  Given fs/local rooted at t.TempDir()
  When fstest.Run executes
  Then every subtest passes on the Linux lane and on the Windows lane
  ```
- **Scenario:** The fence holds through a link swapped mid-write
  ```gherkin
  @TC-01b @proof:service:filesystem @venue:gate @DH-D-FS-4 @edge
  Given a write root R and an outside directory O in t.TempDir()
  And a helper goroutine swapping R/sub for a link to O 1,000 times
  When the seam writes R/sub/f on every iteration
  Then no file ever appears under O (hash listing before equals after)
  And every refused write returns "fs/sandbox-denied" and leaves no staged file
  ```
- **Scenario:** A stale version is refused and the newer bytes survive
  ```gherkin
  @TC-01b @proof:service:filesystem @venue:gate @edge
  Given file f read at version v1 and rewritten to v2 by another handle
  When a write carrying v1 is attempted
  Then it returns "fs/stale-version" and f holds the v2 bytes
  ```

#### Feature: Policy resolver and its model-visible text (SANDBOX-1, C33)

- **Scenario:** Resolver precedence
  ```gherkin
  @TC-01a @proof:in-process @venue:gate
  Given mode workspace-write, working directory W, workspace root P and a grant G
  When Resolve runs
  Then the write roots are exactly {P, G} plus scratch
  And mode read-only yields no write roots
  ```
- **Scenario:** SetMode is logged and announced before it takes effect
  ```gherkin
  @TC-01a @proof:in-process @venue:gate
  Given an in-memory session in workspace-write
  When SetMode(danger-full-access) runs
  Then one "sandbox/mode" event is appended
  And one sandbox.ModeChanging dispatch is observed before the new mode resolves
  ```
- **Scenario:** Policy section renders per platform
  ```gherkin
  @TC-01g @proof:golden @venue:gate
  Given the resolver for linux, darwin and windows and the test-only fixture template
  When policy.Render runs
  Then each output equals testdata/render_fixture_{linux,darwin,windows}.golden byte for byte
  ```
- **Scenario:** No shipped text means no section
  ```gherkin
  @TC-01a @proof:in-process @venue:gate @edge
  Given a build with no world/sandbox/policy/text/ directory
  When the prompt builder composes a session prompt
  Then policy.Text returns policy.ErrNoText and the prompt has no sandbox policy section
  ```
- **Scenario:** Unproven policy text cannot merge (M2-C-6)
  ```gherkin
  @TC-01g @proof:golden @venue:gate @edge
  Given the committed trees under tools/harness-gate/testdata/policytext/
  When "harness-gate policy-text --root <tree>" runs on each
  Then no_text and text_with_matching_pass exit 0
  And text_without_sandbox7 and text_edited_after_pass exit non-zero naming DH-E-SANDBOX-7
  ```

#### Feature: Directory moves and resume move the fence (cycle C-3 integrated)

- **Scenario:** A directory move changes what may be written
  ```gherkin
  @TC-01b @proof:service:filesystem @venue:gate
  Given a booted spine in workspace-write with working directory A in t.TempDir()
  When the host moves the session to B outside A's workspace
  Then a write to A/x returns "fs/sandbox-denied" and a write to B/x succeeds
  ```
- **Scenario:** Resume restores the fence
  ```gherkin
  @TC-01b @proof:service:sqlite @venue:gate
  Given that session persisted in embedded SQLite under t.TempDir()
  When the process reopens the store and resumes
  Then the rebuilt working directory is B and the same write decisions hold
  ```

#### Feature: Permissions choice (HUMAN-4, C34)

- **Scenario:** One switch moves mode and approval policy together
  ```gherkin
  @TC-02a @proof:in-process @venue:gate
  Given a session pinned to preset "ask"
  When "/permission auto" runs
  Then one "sandbox/mode" and one approval-policy event are appended in that order
  And both views read "auto"
  ```
- **Scenario:** An unresolvable default fails configuration load
  ```gherkin
  @TC-02a @proof:in-process @venue:gate @edge
  Given a layer whose default permission preset names a missing preset
  When configuration loads
  Then it fails with a configuration error naming the preset
  ```

#### Feature: Six file tools change files exactly as logged (FS-6, C35)

- **Scenario:** write, edit and apply_patch match the log
  ```gherkin
  @TC-03b @proof:service:filesystem @venue:gate @DH-D-FS-6
  Given a workspace in t.TempDir()
  When write, edit and apply_patch run through the tool pipeline
  Then the files equal the expected bytes
  And each logged tool result names the same path and version
  ```
- **Scenario:** grep needs no rg
  ```gherkin
  @TC-03b @proof:service:filesystem @venue:gate @edge
  Given PATH scrubbed to an empty directory
  When grep searches 1,000 files
  Then it returns matches and the subprocess fake records zero spawns
  ```
- **Scenario:** A large read spills
  ```gherkin
  @TC-03b @proof:service:filesystem @venue:gate @edge
  Given a 5 MiB file
  When read runs with the default bound
  Then the result is truncated at the bound and carries a spill reference whose content equals the whole file
  ```

#### Feature: S12 scenario authoring

- **Scenario:** Five new scenario files compile and are indexed once
  ```gherkin
  @proof:service:go-toolchain @venue:gate
  Given fs_fence_scenario_test.go, fs_long_scenario_test.go, fs_two_process_scenario_test.go, view_workdir_scenario_test.go and sandbox_policy_text_scenario_test.go
  When "go -C src/harness vet -tags scenario ./internal/testkit/scenarios/..." and the scenario-index check run on both lanes
  Then both exit 0
  And DH-E-FS-1, FS-2 and VIEW-5 are indexed under DH-S-18, FS-3 under DH-S-16 and SANDBOX-7 under DH-S-17, once each
  And none of the five is in the default gate run
  ```

---

# Phase 3: Sandbox Launch, Linux Backends and Shell Tools

impl.md DH-S-13, implementation-plan Phase 3. Components C36 (`world/sandbox/launch`,
hidden `dsh sandbox-launch`), C37 (`world/sandbox`, `sandbox/local`), C38
(`world/subprocess` launch plan and rings), C39/C40 (`world/shellexec`, bash and pwsh
tools), C41 (`context/instructions`). Proves TC-04a, TC-04b, TC-05b, TC-06b, TC-07a,
TC-07g. Real confinement is **not** a gate dependency (contract rule 8): the launch
protocol is proven with the `none` backend; bubblewrap and Landlock are proven in Phase 9.
Authors DH-E-SANDBOX-1, NATIVESYSTEM-1..4, CONTEXT-2 (run in Phase 9) and SANDBOX-2,
EXEC-7, EXEC-8 (run at DH-S-39).

### Setup
- **Type**: inline
- **Local**: `go -C src/harness test -race ./internal/world/sandbox/... ./internal/world/subprocess/... ./internal/world/shellexec/... ./internal/context/instructions/...`
  on `lanes/wsl.sh` (bash rows) and `lanes/win.ps1` (pwsh rows).
- **CI runner**: GitHub-hosted `ubuntu-latest` (bash) and `windows-latest` (pwsh,
  shipped on the image), `harness` job of `ci.yml`.
- **Secrets**: none.
- **Pre-test bootstrap**: lane preflight checks the shell of its lane (`bash` on
  Linux, `pwsh` on Windows) and **fails** when absent (architecture.md "Shells").
- **Gate provisioning**: no connection env-var. The launch stage is the test binary
  re-executed as `sandbox-launch` with the `none` backend; "the command" is a
  marker-writing helper whose file proves exec happened or not. Backends and kernel
  evidence are fakes (`TC-04a`). Floods and grandchildren are re-exec helpers reaped by
  `testkit/procwait`. Shell rows that start a command set `danger-full-access` in a test
  layer (the only plan Windows can run in M2, M2-C-8). No scenario skips.

### Scenarios

#### Feature: No command runs before go (C36, INV-14)

- **Scenario:** Host closes after ready -- the command never runs
  ```gherkin
  @TC-04b @proof:service:os-process @venue:gate @DH-T-SANDBOX-4 @edge
  Given the test binary re-executed as sandbox-launch, backend none, command = marker helper
  When the host reads "ready" and closes the channel without "go"
  Then the marker file never exists and the stage exits non-zero
  ```
- **Scenario:** The stage dies before go -- the host reports no command ran
  ```gherkin
  @TC-04b @proof:service:os-process @venue:gate @edge
  Given the same stage killed after sending "ready"
  When the host reads end of file
  Then the outcome is Failed with "sandbox/launch-failed" and the marker is absent
  ```
- **Scenario:** An unconfined plan reaches go
  ```gherkin
  @TC-04b @proof:service:os-process @venue:gate
  Given a danger-full-access plan with backend none
  When the host answers "go"
  Then the marker exists and the reported confinement is Unconfined with "canary: not-tried"
  ```
- **Scenario:** A confined plan whose canary was written is refused
  ```gherkin
  @TC-04b @proof:service:os-process @venue:gate @edge
  Given a confined plan and the test hook that makes the stage report canary "written"
  When Confirm evaluates "ready"
  Then the host closes, the outcome code is "sandbox/launch-failed" and the marker is absent
  ```
- **Scenario:** The same unconfined plan under require full is refused
  ```gherkin
  @TC-04b @proof:service:os-process @venue:gate @DH-T-SANDBOX-14 @edge
  Given a danger-full-access plan and "require: full"
  When Confirm runs
  Then the result is "sandbox/enforcement-insufficient" and the marker is absent (INV-4)
  ```

#### Feature: Probe, select and confirm (C37, SANDBOX-3, SANDBOX-4 logic)

- **Scenario:** The four-step host decision
  ```gherkin
  @TC-04a @proof:in-process @venue:gate
  Given fake backends and the rows confined+written, confined+not-tried, unconfined+not-tried, unconfined+require-full
  When Confirm evaluates each
  Then the results are reject, reject, accept Unconfined, reject "sandbox/enforcement-insufficient"
  ```
- **Scenario:** The weaker evidence wins
  ```gherkin
  @TC-04a @proof:in-process @venue:gate @DH-T-SANDBOX-3
  Given a start-up probe reporting Full and a "ready" that omits the network effect
  When enforcement is computed
  Then it is Partial with Missing [network] (INV-3)
  ```
- **Scenario:** Selection order and refusal
  ```gherkin
  @TC-04a @proof:in-process @venue:gate
  Given fake probes where bwrap fails and Landlock passes
  When the service selects
  Then the backend is landlock
  And with both failing, a confined policy is refused "sandbox/unavailable"
  ```
- **Scenario:** bwrap argv and Landlock rules follow the policy
  ```gherkin
  @TC-04a @proof:golden @venue:gate
  Given a workspace-write policy with roots {P, G}
  When the bwrap backend builds argv and the Landlock backend builds rules for a fake ABI 3 kernel
  Then argv equals world/sandbox/local/testdata/bwrap_argv.golden
  And Landlock grants writes only beneath {P, G, scratch} and lists the ABI-gated effects ABI 3 lacks in Missing
  ```
- **Scenario:** Windows refuses a confined start
  ```gherkin
  @TC-06b @proof:service:os-process @venue:gate @platform:windows @edge
  Given the Windows lane and policy workspace-write
  When Prepare runs
  Then it returns "sandbox/unavailable" and no process starts (M2-C-8, R-05)
  ```

#### Feature: A command starts under its plan or not at all (C38, EXEC-3, EXEC-4)

- **Scenario:** A Spec without a launch plan is refused
  ```gherkin
  @TC-04a @proof:in-process @venue:gate @DH-T-EXEC-8 @edge
  Given subprocess.Spec with Argv and no Launch
  When Start is called
  Then it returns an herr error and the fake spawner records zero starts (INV-2)
  ```
- **Scenario:** M1 client routes still start through the one spawn place
  ```gherkin
  @proof:service:os-process @venue:gate
  Given the existing claudecode fake-CLI tests
  When they run after the change
  Then they pass and each recorded spawn carries a helper plan (M2-NG-3)
  ```
- **Scenario:** A flood is bounded and recoverable
  ```gherkin
  @TC-05b @proof:service:os-process @venue:gate @DH-T-EXEC-7
  Given a re-exec helper writing 64 MiB to stdout
  When it runs under the default ring size
  Then ring memory stays at its bound, the tail equals the last bytes written, and the spill file holds the full stream (INV-6)
  ```
- **Scenario:** The hard limit ends the whole tree
  ```gherkin
  @TC-05b @proof:service:os-process @venue:gate @edge
  Given a helper that spawns a grandchild and sleeps
  When Run's hard limit fires
  Then both pids are gone within the bound as seen by testkit/procwait
  ```

#### Feature: bash and pwsh executors and tools (C39, C40)

- **Scenario:** bash on the Linux lane
  ```gherkin
  @TC-06b @proof:service:os-process @venue:gate @platform:linux-wsl
  Given bash found by preflight and a danger-full-access test layer
  When the bash tool runs "echo hi; exit 3"
  Then the logged result has exit code 3, stdout tail "hi" and confinement.state Unconfined
  ```
- **Scenario:** pwsh on the Windows lane
  ```gherkin
  @TC-06b @proof:service:os-process @venue:gate @platform:windows
  Given pwsh found by preflight and a danger-full-access test layer
  When the pwsh tool runs "Write-Output hi; exit 3"
  Then the logged result has exit code 3 and tail "hi"
  ```
- **Scenario:** The model cannot pass stdin or environment
  ```gherkin
  @TC-06b @proof:in-process @venue:gate @edge
  Given a tool call with arguments {command, stdin}
  When the tool validates input
  Then it returns a validation error naming "stdin" and nothing starts
  ```
- **Scenario:** Secret names never reach the child
  ```gherkin
  @TC-06b @proof:in-process @venue:gate @DH-T-EXEC-13 @edge
  Given a secret named DSH_TEST_TOKEN in human/secret and in the parent environment
  When shellexec/env builds the child environment
  Then the child environment has no DSH_TEST_TOKEN (INV-11)
  ```

#### Feature: Instruction files through the seam (C41, CONTEXT-2)

- **Scenario:** A link outside the root is refused
  ```gherkin
  @TC-07a @proof:in-process @venue:gate @edge
  Given a fake seam where AGENTS.md is a link outside the root
  When discovery runs
  Then the file is skipped, "fs/sandbox-denied" is recorded in instructions.visible.refused, and the section omits it
  ```
- **Scenario:** The rendered section is framed and budgeted
  ```gherkin
  @TC-07g @proof:golden @venue:gate
  Given three nested instruction files exceeding the budget
  When the section renders
  Then it equals context/instructions/testdata/section.golden with frames escaped and truncation marked
  ```
- **Scenario:** An outside edit is reconciled
  ```gherkin
  @TC-07a @proof:service:filesystem @venue:gate
  Given a t.TempDir() workspace with AGENTS.md
  When the file is rewritten
  Then within the watch debounce the fold digest changes and the next section holds the new text
  ```

#### Feature: S13 scenario authoring

- **Scenario:** Nine files compile and are indexed under their executing stages
  ```gherkin
  @proof:service:go-toolchain @venue:gate
  Given sandbox_native_scenario_test.go, sandbox_status_scenario_test.go, context_instructions_scenario_test.go and the three later-run files for SANDBOX-2, EXEC-7, EXEC-8
  When "go vet -tags scenario" and the scenario-index check run on both lanes
  Then both exit 0
  And SANDBOX-1, NATIVESYSTEM-1..4 and CONTEXT-2 are indexed under DH-S-18 and SANDBOX-2, EXEC-7, EXEC-8 under DH-S-39, once each
  ```

# Phase 4: Agent Presets and Bundles

impl.md DH-S-14, implementation-plan Phase 4 (Stages 4.1-4.6). The components are:
- C42, profiles and layers, in the existing package `boot/profile`: `profile.go`,
  `stack.go`, `bundles/*.yml`, and `testdata/composed.golden`.
- C43, the preset registry and persona, in the new `boot/agentpreset`:
  `agentpreset/persona` and `presets/standard.yml`.

The phase proves TC-08g, TC-08a and TC-09b. `bundles/minimal.yml` and `bundles/acp.yml`
are not among the five profiles and stay untouched.

### Setup
- **Type**: inline
- **Local**: `go -C src/harness test -race -shuffle=on ./internal/boot/profile/... ./internal/boot/agentpreset/...`
  on both lanes (`lanes/win.ps1`, `lanes/wsl.sh`). A step that changes composition
  regenerates `internal/boot/profile/testdata/composed.golden` with `-update` in the
  same commit, and the diff is reviewed (architecture.md rule 5).
- **CI runner**: GitHub-hosted `ubuntu-latest` and `windows-latest`, `harness` job of `ci.yml`.
- **Secrets**: none.
- **Pre-test bootstrap**: none.
- **Gate provisioning**: there is no connection env-var.
  - Composition rows read the committed bundles and golden.
  - Catalogue, coverage, `Extends` and persona rows are in-memory.
  - The revision/lease rows and the real-tool composition row use embedded modernc
    SQLite under `t.TempDir()`.
  - The real-tool row needs the six file tools (Phase 2) and the `bash`/`pwsh` tools
    (Phase 3) already merged. It is ordered by the plan's cross-stage dependencies and
    never skips.

### Scenarios

#### Feature: Five profiles compose deterministically (C42, BUNDLE-1, -2, -3)

- **Scenario:** Composed profiles match the golden
  ```gherkin
  @TC-08g @proof:golden @venue:gate @DH-D-BUNDLE-1
  Given the five profile definitions admin, core, web, headless and sdk in boot/profile
  And bundles/headless.yml and bundles/sdk.yml filled with their membership
  When the composer renders each profile on the Linux lane and on the Windows lane
  Then each output equals internal/boot/profile/testdata/composed.golden byte for byte
  ```
- **Scenario:** A missing Required component fails composition
  ```gherkin
  @TC-08a @proof:in-process @venue:gate @edge
  Given an in-memory headless bundle lacking one entry of the headless Required list
  When composition runs
  Then it fails naming the missing component
  ```
- **Scenario:** Layer operations record their source
  ```gherkin
  @TC-08a @proof:in-process @venue:gate @DH-D-BUNDLE-2
  Given base, then admin, then a user layer that removes one entry and merges a map
  When boot/profile/stack.go stacks them
  Then the entry is removed, the map is merged key-wise, and each value records the layer that supplied it
  ```
- **Scenario:** The admin profile holds no agent
  ```gherkin
  @TC-08g @proof:golden @venue:gate @edge
  Given the committed admin.yml, base.yml and core.yml
  When admin composes
  Then it equals its section of composed.golden
  And it contains no agent, model adapter or carrier component (INV-10)
  ```

#### Feature: Every catalogue entry is placed and defaults send nothing (C42, BUNDLE-6)

- **Scenario:** An unbundled catalogue entry fails coverage
  ```gherkin
  @TC-08a @proof:in-process @venue:gate @DH-D-BUNDLE-6 @edge
  Given an in-memory catalogue with component x in no profile and not in bundles/optional.yml
  When the coverage check runs
  Then it fails naming x
  And adding x to optional.yml makes it pass
  ```
- **Scenario:** Default profiles send nothing off the machine
  ```gherkin
  @TC-08a @proof:in-process @venue:gate
  Given each default profile composed with no route configured
  When the egress set is computed
  Then it is empty (NG-7)
  ```

#### Feature: Presets keep their revision (C43, BUNDLE-4, -5)

- **Scenario:** A session keeps the revision it bound
  ```gherkin
  @TC-09b @proof:service:sqlite @venue:gate @DH-D-BUNDLE-4
  Given a session bound to preset standard rev 1 in embedded SQLite under t.TempDir()
  When the preset is edited to rev 2
  Then the running session's fold still reads rev 1 and rev 1's lease count is 1
  And a new session binds rev 2
  ```
- **Scenario:** An Extends cycle is refused
  ```gherkin
  @TC-09b @proof:in-process @venue:gate @edge
  Given in-memory declarations "a extends b" and "b extends a"
  When the registry loads
  Then it fails naming both a and b
  ```
- **Scenario:** Persona outside a preset is refused
  ```gherkin
  @TC-09b @proof:in-process @venue:gate @DH-D-BUNDLE-5 @edge
  Given a configuration enabling agentpreset/persona at top level
  When boot loads it
  Then load fails with a configuration error naming persona
  ```
- **Scenario:** The standard preset declares exactly the M2 tools
  ```gherkin
  @TC-09b @proof:in-process @venue:gate
  Given the committed boot/agentpreset/presets/standard.yml and fake registrations for eight tool names
  When the registry resolves standard
  Then its declared tool list is exactly "read write edit apply_patch glob grep bash pwsh"
  And a fixture preset naming an unknown tool is refused naming it
  ```
- **Scenario:** The standard preset composes an agent with the real tools
  ```gherkin
  @TC-09b @proof:service:sqlite @venue:gate
  Given standard.yml, the six file tools (C35) and the bash and pwsh tools (C40) registered by their real constructors
  When an agent is created in an embedded store under t.TempDir()
  Then its tool list is exactly "read write edit apply_patch glob grep bash pwsh" and the binding is persisted
  ```

#### Feature: S14 built record

- **Scenario:** S14 gate green and record complete
  ```gherkin
  @TC-17a @proof:golden @venue:gate
  Given the stage's merged commits and evidence/DH-S-14.md still reading "Closure status: open"
  When harness-gate runs on both lanes and "harness-gate ready --stage DH-S-14 --built" runs
  Then every gate including the coverage check passes
  And ready exits 0 with no missing manual check or commit field
  ```

---

# Phase 5: Listener, Sign-In, Stdio and SDK Server

impl.md DH-S-15, implementation-plan Phase 5 (Stages 5.1-5.9). The components are:

| Id | Component | Package |
|---|---|---|
| C44 | Listener and assets | `host/listen`, `host/assets` |
| C45 | Admission | `host/auth` |
| C46 | Host launch and control | `host/launch` |
| C47 | Routes | `host/web` |
| C48 | Stdio owner | `wire/stdio` |
| C49 | SDK server | `wire/sdkserver`: `server.go`, `forward.go`, `runs.go`, `answer.go` |

The phase proves TC-10b, TC-11b (runtime record and control endpoint only), TC-12a,
TC-12b (helper-process form) and TC-12g. The `dsh sdk` launcher, `testkit/dshbin` and
the replay provider do not exist yet (Phase 6). So every process row here uses the test
binary re-executed as a helper through `world/subprocess.Start`, and every protocol row
uses a `jsonrpctest` peer with a fake model. The same endings on built `dsh --profile sdk`
are asserted in Phase 6. The phase also authors the later-run HOST-1, -2, -7, -8 and -9.

### Setup
- **Type**: inline
- **Local**:
  - `go -C src/harness test -race ./internal/host/... ./internal/wire/...` on both lanes;
  - `go -C src/sdk test ./protocol/...` (`protocol/internal/genschema` regenerates
    `protocol/schema.json`, which must not change);
  - `go -C src/harness vet -tags scenario ./internal/testkit/scenarios/...` for the
    authoring check.
- **CI runner**: GitHub-hosted `ubuntu-latest` and `windows-latest`, `harness` job.
- **Secrets**: none. Launch tokens, cookies and certificate pairs are generated in memory per test.
- **Pre-test bootstrap**: none.
- **Gate provisioning**: there is no connection env-var.
  - The listener binds `127.0.0.1:0`.
  - The control endpoint is a same-user local socket under `t.TempDir()` (AF_UNIX on
    Windows).
  - The hashed session table is embedded SQLite in a temp home.
  - Stdio rows re-exec the test binary (`-test.run=TestHelperStdioOwner`,
    `GO_WANT_HELPER=1`) through `world/subprocess.Start` (M2-NG-3; no `os/exec`).
  - SDK rows use `src/sdk/jsonrpc/jsonrpctest`, `testkit/model` and memstore.
  - No row starts built `dsh`, `testkit/dshbin` or a replay provider, and no row skips.

### Scenarios

#### Feature: Listener and admission (C44, C45; HOST-1, HOST-2)

- **Scenario:** Longest-prefix mount, only after admission
  ```gherkin
  @TC-10b @proof:service:loopback-http @venue:gate @DH-D-HOST-1
  Given mounts /api and /api/sdk on 127.0.0.1:0 with an admit-all fake
  When "GET /api/sdk/x" is sent
  Then the /api/sdk handler answers
  And with an admit-none fake the response is 401 and no handler runs
  ```
- **Scenario:** TLS reloads without restart
  ```gherkin
  @TC-10b @proof:service:loopback-http @venue:gate
  Given a per-test certificate pair in t.TempDir()
  When the files are replaced with a new pair
  Then a new connection presents the new certificate and the process did not restart
  ```
- **Scenario:** The launch token is one-time
  ```gherkin
  @TC-10b @proof:service:loopback-http @venue:gate @DH-D-HOST-2 @edge
  Given a launch token issued by host/auth
  When it is exchanged twice
  Then the first exchange sets a cookie and the second returns 401
  ```
- **Scenario:** Refusals by cookie, origin and source
  ```gherkin
  @TC-10b @proof:service:loopback-http @venue:gate @edge
  Given the listener
  When requests arrive without the cookie, with a foreign Origin, and from a source that is neither loopback nor an allowed peer
  Then each is refused 401 or 403 and none reaches a mount
  ```

#### Feature: Host launch, control endpoint and route table (C46, C47; HOST-3, API-1)

- **Scenario:** Only a listening host writes a runtime record
  ```gherkin
  @TC-11b @proof:service:filesystem @venue:gate @DH-D-HOST-3
  Given a temp home
  When a listener profile and a headless profile boot in-test
  Then <home>/run/ holds exactly one runtime record, the listener's (M2-C-13)
  ```
- **Scenario:** The control endpoint returns no credential
  ```gherkin
  @TC-11b @proof:service:local-socket @venue:gate @edge
  Given a running host
  When host.status is called over the local socket
  Then the reply holds the address and pid and no token or cookie field
  ```
- **Scenario:** An undeclared route is refused
  ```gherkin
  @TC-10b @proof:in-process @venue:gate @DH-D-API-1 @edge
  Given a route without an effect declaration
  When Routes.Handle registers it
  Then it returns an error and the route table is unchanged
  ```
- **Scenario:** Component cleanup cancels in-flight handlers
  ```gherkin
  @TC-10b @proof:service:loopback-http @venue:gate
  Given a slow handler serving a request on 127.0.0.1:0
  When its component is disposed
  Then the handler's context is cancelled and a new request gets 404
  ```

#### Feature: Stdio owner with bounded exit (C48, SDK-3)

- **Scenario:** A stray stdout write goes to the log
  ```gherkin
  @TC-12b @proof:service:os-process @venue:gate @DH-D-SDK-3
  Given the test binary re-executed as a helper that claims stdio with wire/stdio, writes one frame, then calls fmt.Println("x")
  And the parent starts it through world/subprocess.Start with stdout bound to a buffer
  When the helper exits
  Then stdout holds only the one frame and the helper's log in t.TempDir() holds "x"
  ```
- **Scenario:** The bounded writer reports a stalled client once
  ```gherkin
  @TC-12a @proof:in-process @venue:gate @edge
  Given a bounded writer over an io.Pipe whose reader never reads
  When the write deadline elapses
  Then the writer reports ClientStalled and calls the injected stop function exactly once
  ```
- **Scenario:** Bounded endings 0, 125, 124 on the helper
  ```gherkin
  @TC-12b @proof:service:os-process @venue:gate @edge
  Given the re-executed helper owning real stdio, an injected stop that exits with its status, and a 2 s forced deadline
  When the parent closes the helper's stdin; when the parent stops reading while the helper writes; when the deadline elapses with stdin open
  Then the exit statuses are 0, 125 and 124 respectively (M2-C-10)
  ```

#### Feature: SDK server methods, results and answerer (C49; SDK-4, -5, -6)

- **Scenario:** Create, prompt and history
  ```gherkin
  @TC-12a @proof:in-process @venue:gate @DH-D-SDK-4
  Given a jsonrpctest peer, a testkit/model fake and a memstore
  When the client creates a session, prompts and calls history
  Then history holds the user and assistant messages in order
  ```
- **Scenario:** Resume of a live-owned session is refused
  ```gherkin
  @TC-12a @proof:in-process @venue:gate @edge
  Given a session owned by a live writer
  When a second connection resumes it
  Then the reply is "session/owned"
  ```
- **Scenario:** The wire schema is unchanged
  ```gherkin
  @TC-12g @proof:golden @venue:gate
  Given src/sdk/protocol/schema.json
  When src/sdk/protocol/internal/genschema regenerates it
  Then the output is byte-identical to the committed file (M2-C-7; H-1, H-2, H-3 recorded "not built")
  ```
- **Scenario:** Scoped, ordered notifications
  ```gherkin
  @TC-12a @proof:in-process @venue:gate @DH-D-SDK-5
  Given two sessions on one connection
  When both run
  Then each session.event carries its session id
  And per-session sequence numbers strictly increase with no cross-session leakage
  ```
- **Scenario:** The run result equals the last assistant text
  ```gherkin
  @TC-12a @proof:in-process @venue:gate
  Given a fake model replying "A" then "B" in one run
  When session.run.completed arrives
  Then its result text is "B"
  ```
- **Scenario:** An invalid answer is rejected
  ```gherkin
  @TC-12a @proof:in-process @venue:gate @DH-D-SDK-6 @edge
  Given a pending approval/request
  When the client answers with an option not offered
  Then the server returns an sdk/* error and the approval stays pending
  ```
- **Scenario:** Withdrawal cancels a question
  ```gherkin
  @TC-12a @proof:in-process @venue:gate @edge
  Given a pending question/ask
  When the turn is cancelled
  Then the client receives the withdrawal and a late answer is rejected
  ```
- **Scenario:** No session grant survives the connection
  ```gherkin
  @TC-12a @proof:in-process @venue:gate @edge
  Given an approval answered "allow for session" on connection 1
  When connection 2 resumes the session
  Then the next identical call asks again (NG-8)
  ```

#### Feature: S15 scenario authoring and record

- **Scenario:** Authored S15 scenarios compile and are indexed
  ```gherkin
  @proof:service:go-toolchain @venue:gate
  Given the five new HOST files built on testkit/scenarios/binary_test.go (dshBinary over testkit/launcher.Build) and raw JSON-RPC from src/sdk/jsonrpc
  When "go vet -tags scenario" and the scenario-index check run on both lanes
  Then both exit 0
  And HOST-1 and HOST-2 are indexed once under DH-S-22 and HOST-7, HOST-8, HOST-9 once under DH-S-39
  And none imports src/sdk/sdk (M2-DOD-5)
  ```
- **Scenario:** S15 gate green on both lanes
  ```gherkin
  @proof:service:go-toolchain @venue:gate
  Given the stage's merged commits
  When harness-gate runs on both lanes
  Then every gate passes and TC-12g is unchanged (M2-DOD-1)
  ```

---

# Phase 6: Profiles, Launchers and Evidence Runners

impl.md DH-S-16 steps 16.1-16.7, 16.9 and 16.10; implementation-plan Phase 6, Stages
6.1-6.11. The components are:

| Id | Component | Package |
|---|---|---|
| C42 | Mode layers and five launchable profiles | `boot/profile` |
| C50 | CLI launchers and admin tree | `cli` (`tree.go`, architecture.md I-3), `cli/headless` |
| C51 | Lifecycle | `cli/lifecycle` |
| C52 | Host client | `cli/hostclient` |
| C53 | Go client | `src/sdk/sdk` |
| C54 | Keyless replay | `llm/replay`, `testkit/fixture`, `testkit/dshbin`, `test/replay` |
| C55 | Scenario helper | `testkit/scenario` |
| C56 | Titles | `session/title` |

Proof rows:

| Row | What this phase proves for it |
|---|---|
| TC-08g | Mode layers |
| TC-11b | Second launch and crash notice |
| TC-12b | Built-binary form |
| TC-13b, TC-13g | Admin tree, headless and command reference |
| TC-14b | One bounded stop |
| TC-15b | Go client |
| TC-16g, TC-16b | Replay oracle and its execution |
| TC-17a | Scenario helper and analyzer |
| TC-18a | Titles |

The four step 16.8 guarded runs are Phase 7.

### Setup
- **Type**: inline
- **Local**:
  - `go -C src/harness test -race ./internal/boot/... ./internal/cli/... ./internal/session/title/... ./internal/llm/replay/... ./internal/testkit/...`;
  - `go -C src/sdk test -race ./sdk/...`;
  - `GOWORK=off go -C tools/harness-gate test ./...` (analyzer, preflight parser);
  - the `harness-replay` gate step (decision `replay-gate-step-dh-s-16`), run by both
    lane scripts.

  All of it runs on both lanes.
- **CI runner**: GitHub-hosted `ubuntu-latest` and `windows-latest`, `harness` job of `ci.yml`.
- **Secrets**: none. Every model call in this phase is the keyless fake route or the
  committed replay recording.
- **Pre-test bootstrap**: none. The lane preflight checks Go 1.27.4 and its own shell:
  `bash` on WSL, `pwsh` on Windows (Stage 6.10).
- **Gate provisioning**: there is no connection env-var.
  - **Built binary.** Stages 6.2-6.7 build `dsh` with the M1 `testkit/launcher.Build`
    of `./cmd/dsh`; from Stage 6.8 onward they use `testkit/dshbin`, which wraps it
    (once per test binary, into `t.TempDir()`). `DSH_BIN` is set by the test to that
    binary.
  - **Model.** The keyless fake route, configured by a test layer, or `llm/replay` over
    the committed `test/replay` recording.
  - **Homes.** Fresh `t.TempDir()` homes with embedded SQLite. `testkit/fixture` copies
    the replay workspace.
  - **Signals.** Real on the Linux lane; console control events on the Windows lane
    (`terminate_windows_test.go` pattern).
  - Nothing is downloaded and nothing is installed. No row skips.

### Scenarios

#### Feature: Mode layers and five launchable profiles (C42, BUNDLE-3)

- **Scenario:** Each profile boots and only web binds a port
  ```gherkin
  @TC-08g @TC-11b @proof:service:os-process @venue:gate @DH-D-BUNDLE-3
  Given a temp home and the keyless fake route
  When boot.Process runs admin, core, web, headless and sdk in turn in-test
  Then each reaches ready and stops cleanly
  And only web binds a port, and headless and sdk write no runtime record (M2-C-13)
  ```
- **Scenario:** Mode-layer composition equals the golden
  ```gherkin
  @TC-08g @proof:golden @venue:gate
  Given the committed mode layers
  When composed
  Then the output equals internal/boot/profile/testdata/composed.golden
  ```

#### Feature: Admin tree, secrets and configuration preview (C50; CLI-9, BOOT-6 / H-21)

- **Scenario:** The admin tree builds no agent
  ```gherkin
  @TC-13b @proof:service:os-process @venue:gate @DH-D-CLI-9
  Given built dsh (testkit/launcher.Build) and a temp home
  When "dsh sandbox status" runs
  Then it exits 0, prints the C37 report (Full, Partial or Unconfined; backend; missing and exposed effects)
  And its component trace lists no agent, model client or carrier (INV-10, M2-C-14)
  ```
- **Scenario:** A secret is read from stdin only
  ```gherkin
  @TC-13b @proof:service:os-process @venue:gate @edge
  Given "dsh secret set k" with "v" on stdin in a scratch home
  When it runs
  Then the secret store holds k
  And "dsh secret set k v" with the value in argv is rejected with a usage error (M2-C-11)
  ```
- **Scenario:** The command reference equals the golden
  ```gherkin
  @TC-13g @proof:golden @venue:gate
  Given the command tree with the launchers and the admin tree
  When the reference is rendered
  Then it equals internal/cli/testdata/reference.golden
  ```
- **Scenario:** The configuration preview constructs no component
  ```gherkin
  @TC-13b @proof:service:os-process @venue:gate @DH-D-BOOT-6
  Given a temp home with a user layer
  When "dsh config show --profile headless" runs
  Then it prints each value with its source layer and the component trace is empty
  ```
- **Scenario:** Validate rejects bad configuration with 78
  ```gherkin
  @TC-13b @proof:service:os-process @venue:gate @edge
  Given a layer whose default permission preset cannot be resolved
  When "dsh config validate" runs
  Then it exits 78 naming the preset, and H-21 is recorded closed in evidence/closure.md
  ```

#### Feature: dsh headless and dsh sdk launchers (C50, C48 wired; CLI-2)

- **Scenario:** headless prints the last assistant text
  ```gherkin
  @TC-13b @proof:service:os-process @venue:gate @DH-D-CLI-2
  Given built dsh with the keyless fake route answering "hello" and a temp home
  When "dsh headless 'say hello'" runs
  Then stdout is exactly "hello\n" and the exit status is 0
  ```
- **Scenario:** --json emits export lines
  ```gherkin
  @TC-13b @proof:service:os-process @venue:gate
  Given the same run with --json
  When it completes
  Then every stdout line parses as an export record and the last one is the run result
  ```
- **Scenario:** An admin command runs beside a live headless run
  ```gherkin
  @TC-13b @proof:service:os-process @venue:gate @edge
  Given a dsh headless run on a temp home blocked on a fake-model reply
  When "dsh sandbox status" runs on the same home
  Then it exits 0 with no agent, model client or carrier in its trace
  And the headless run then completes with status 0 (INV-10)
  ```
- **Scenario:** SDK launcher bounded endings on the built binary
  ```gherkin
  @TC-12b @proof:service:os-process @venue:gate @edge
  Given built "dsh --profile sdk" with the keyless fake route from a test layer
  When stdin closes; when the client stops reading; when the forced deadline elapses
  Then the exit statuses are 0, 125 and 124 respectively (M2-C-10; exit 130 stays open item I-6)
  ```

#### Feature: One bounded stop path (C51, CLI-4)

- **Scenario:** Every trigger reaches one Stop
  ```gherkin
  @TC-14b @proof:in-process @venue:gate @DH-D-CLI-4
  Given lifecycle.Watch with a counting fake Stop
  When Ctrl-C, SIGTERM, SIGHUP, stdin end, "dsh stop" and stdio end each fire in separate runs
  Then Stop is called exactly once per run
  And a second Ctrl-C moves the run to forced
  ```
- **Scenario:** A signal during a tool call ends within ShutdownTimeout
  ```gherkin
  @TC-14b @proof:service:os-process @venue:gate @edge
  Given dsh headless running a long fake tool
  When SIGTERM is sent on the Linux lane, or a console close event on the Windows lane
  Then the process exits within ShutdownTimeout
  And the log closes the turn
  ```

#### Feature: Second launch and crash notice (C52, CLI-5)

- **Scenario:** A second dsh web reuses the running host
  ```gherkin
  @TC-11b @proof:service:os-process @proof:service:local-socket @venue:gate @DH-D-CLI-5
  Given built "dsh web" running on a temp home
  When a second "dsh web" starts on the same home
  Then it exits 0 after launch.open and only one listener exists
  ```
- **Scenario:** An unclean exit is explained at the next start
  ```gherkin
  @TC-11b @proof:service:os-process @venue:gate @edge
  Given a first host killed with SIGKILL (Linux) or TerminateProcess (Windows)
  When "dsh status" runs
  Then it names the crash report path and suggests --safe
  ```

#### Feature: Go client (C53, SDK-8)

- **Scenario:** The client drives the built binary
  ```gherkin
  @TC-15b @proof:service:os-process @proof:service:go-toolchain @venue:gate @DH-D-SDK-8
  Given DSH_BIN set to the binary built once by the test and the keyless fake route
  When src/sdk/sdk starts, creates a session and Runs a prompt
  Then Run returns the fake's text and Close exits the child with status 0
  ```
- **Scenario:** The shutdown ladder escalates
  ```gherkin
  @TC-15b @proof:service:os-process @venue:gate @edge
  Given a child that ignores "shutdown"
  When the client closes with a short grace
  Then it terminates, and failing that kills, and testkit/procwait sees the pid gone
  ```
- **Scenario:** A missing home is refused
  ```gherkin
  @TC-15b @proof:in-process @venue:gate @edge
  Given Options without a home
  When Start is called
  Then it returns an error and no process starts
  ```

#### Feature: Scenario helper, analyzer and keyless replay (C55, C54; UTIL-8, UTIL-7)

- **Scenario:** A missing input fails, not skips
  ```gherkin
  @TC-17a @proof:in-process @venue:gate @edge
  Given a scenario requiring DSH_BIN, with DSH_BIN unset and the scenario listed in DSH_REQUIRE_SCENARIOS
  When it runs
  Then it fails with "required scenario skipped" (M2-DEC-3)
  ```
- **Scenario:** Scenario files may not import product internals
  ```gherkin
  @TC-17a @proof:service:go-toolchain @venue:gate @edge
  Given an analysistest fixture scenario importing internal/agent
  When the analyzer in tools/harness-gate/analyzers runs
  Then it reports exactly one diagnostic
  ```
- **Scenario:** The replay oracle holds byte for byte
  ```gherkin
  @TC-16g @TC-16b @proof:golden @venue:gate @DH-D-UTIL-7
  Given the committed recording and expected files under test/replay
  When the built binary (testkit/dshbin) replays it into a scratch home and a testkit/fixture workspace
  Then the normalized persisted log read back from the SQLite store equals the expected log
  And the workspace tree and the model requests equal their expected files (H-10 recorded open)
  ```
- **Scenario:** Existing scenarios retargeted without rewriting their bodies
  ```gherkin
  @TC-17a @proof:service:go-toolchain @venue:gate
  Given binary_test.go, sdkhost_test.go and realenv_test.go retargeted to testkit/scenario
  When "go vet -tags scenario ./internal/testkit/scenarios/..." runs
  Then it exits 0
  And store_crash_recovery_scenario_test.go no longer reads DSH_SCENARIO_STORE2_KEY (M2-I-1)
  ```

#### Feature: Session titles (C56, VIEW-4)

- **Scenario:** Fallback, then a generated title
  ```gherkin
  @TC-18a @proof:in-process @venue:gate @DH-D-VIEW-4
  Given a session with a replay provider (C54) recording a title reply, and a memstore
  When the first turn completes
  Then the fold first shows the fallback title and then the generated title
  And the log holds session/title-request, session/title-attempt and session/title in that order
  ```
- **Scenario:** A pinned title is not overwritten
  ```gherkin
  @TC-18a @proof:in-process @venue:gate @edge
  Given a renamed (pinned) session
  When Refresh runs
  Then the title is unchanged until Unpin
  ```

#### Feature: Lane preflight and later-run authoring (Stages 6.10, 6.11)

- **Scenario:** The bwrap preflight parser accepts only bwrap Full
  ```gherkin
  @proof:in-process @venue:gate @edge
  Given recorded "dsh sandbox status" outputs "bwrap Full", "landlock Partial" and "Unconfined"
  When the guarded preflight parser evaluates them
  Then only "bwrap Full" passes (architecture.md I-8)
  ```
- **Scenario:** Later-run scenarios compile and are indexed once
  ```gherkin
  @proof:service:go-toolchain @venue:gate
  Given the 17 later-run files of tech-spec sec. 4.5 plus sdk_stdio_scenario_test.go
  When "go vet -tags scenario ./internal/testkit/scenarios/..." runs on both lanes
  Then it exits 0
  And SDK-1 is indexed under DH-S-17, SDK-2 and UTIL-2 under DH-S-39, CLI-1 and CLI-2 under DH-S-27, BUNDLE-3 under DH-S-22, and BUNDLE-1, -2, -4 and API-5 under DH-S-48
  And each of the 17 ids appears exactly once and none under DH-S-16..18
  ```

---

# Phase 7: Guarded Scenarios on the Lane Machines

impl.md DH-S-16 step 16.8, implementation-plan Stage 6.12. It runs four scenarios:
- DH-E-STORE-1, DH-E-STORE-6 and DH-E-FS-3 on both lanes (TC-19s1);
- DH-E-STORE-2 on the WSL lane only (TC-19s2). impl.md 16.8 binds it to Linux; a
  Windows STORE-2 belongs to DH-S-27.

The guarded mode of each lane script runs, in order:
1. `harness-gate entry`;
2. `harness-gate require --stage DH-S-16`, which prints all four ids;
3. `go test -tags scenario` for those ids, on scratch homes (M2-C-1, M2-EN-6).

S16 closes only when every in-scope row reads passed, and Phase 7 completes before any
Phase 8 or 9 run.

### Setup
- **Type**: lab-bare-metal
- **Local**: on each lane machine:
  - WSL: `tools/harness-gate/lanes/wsl.sh --guarded`. Its preflight
    (`--guarded-preflight`) must print backend `bwrap`, state `Full`; `claude` must be
    signed in, and the editor save script installed.
  - Windows: `tools/harness-gate/lanes/win.ps1 -Guarded`. `pwsh` and the editor save
    script must be present.

  Both lanes run
  `go -C src/harness test -tags scenario -p 1 -run '^(TestTwoProcessesShareOneHomeWithOneWriter|TestDeletedSessionLeavesNothingBehind|TestTwoProcessesAndAnEditorLoseNoEdit|TestKilledHostRecoversHonestly)$' ./internal/testkit/scenarios/`
  with `DSH_REQUIRE_SCENARIOS` set to the `require` output.
  - **Selector.** The lane script derives the `-run` selector from the required ids
    through each file's `//dsh:scenario` directive, so selector and list cannot disagree.
  - **Missing statements.** After the run, the C55 statement check fails any required
    id that wrote no evidence statement as `required scenario not run`. A selector that
    matched nothing therefore fails, never succeeds.
  - **Windows lane.** It removes DH-E-STORE-2 from the list and its selector, and
    records that row `out of scope (impl.md 16.8 Linux only; TC-19s2)`.
- **CI runner**: the two lane machines, registered as:
  - ADO pool `forge-lab-hw`;
  - GitHub self-hosted runner labels
    `[self-hosted, linux, wsl, dsh-lane-wsl, dsh-bwrap, dsh-signed-in-claude, dsh-editor]`
    and `[self-hosted, windows, dsh-lane-win, dsh-editor]`.

  Triggered by `.github/workflows/harness-scenarios.yml` (`workflow_dispatch`,
  `stage: DH-S-16`). Its `preflight` job stays on `ubuntu-latest`, and the `scenarios`
  matrix targets the two labels.
- **Secrets**: no model key exists or is stored (M2-NG-2, M2-C-11); STORE-2 uses the
  tester's signed-in client. Machine-access secrets only:
  - Azure Key Vault `kv-forge-dsh-lab`, entry `dsh-lab-runner-token`, exposed through
    ADO variable group `dsh-m2-lab`.
  - GitHub environment `dsh-m2-guarded`, secret `DSH_LAB_RUNNER_TOKEN`.
  - `DSH_SCENARIO_CLAUDE_CODE_MODEL` and `DSH_SCENARIO_CLAUDE_CODE_EFFORT` are
    environment variables, not secrets.
  - These names are the binding contract for this phase and for Phases 8 and 9: one vault
    (`kv-forge-dsh-lab`), one ADO variable group (`dsh-m2-lab`), and one GitHub
    environment per closure stage (`dsh-m2-guarded`, `dsh-m2-s17`, `dsh-m2-s18`), so that
    each stage's approval is independent. Provisioning them is a precondition of the
    `harness-scenarios.yml` dispatch. A missing secret fails the job before any scenario
    runs and the rows read `not run (runner secret missing)`.
- **Pre-test bootstrap**: the lane preflight fails the run, never skips it, when any
  check misses:
  - Both lanes: Go 1.27.4; the lane shell; `DSH_SCENARIO_FS3_EDITOR` pointing at the
    installed editor save script (`service:git-and-editor`); the `DSH_SCENARIO_STORE1_*`
    and `DSH_SCENARIO_STORE6_*` templates exported.
  - WSL lane also: `bwrap` on an absolute path with `dsh sandbox status` reporting
    `bwrap Full` (Stage 6.10, `service:linux-sandbox-bwrap`); `claude` signed in,
    checked by a probe turn; the five `DSH_SCENARIO_STORE2_*` templates.

  Each miss is recorded `not run (<check> preflight failed)`.
- **Gate provisioning**: not applicable, because this phase is `lab-bare-metal`. No row
  enters a gate required list. A missing lane service is a preflight failure recorded
  *not run*, and S16 stays open.

### Scenarios

#### Feature: Two headless hosts on one home (DH-E-STORE-1)

- **Scenario:** Ownership is exclusive and takeover is clean
  ```gherkin
  @TC-19s1 @DH-E-STORE-1 @proof:service:sqlite @venue:guarded @platform:linux-wsl @platform:windows
  Given two built "dsh headless" processes A and B on one scratch home with the keyless replay route, each step limited to 60 s
  And A has created a session and runs a long turn
  When B resumes A's live session
  Then B is refused "session/owned" naming A's pid
  When A is killed without a clean stop and B resumes
  Then B owns the session with lease epoch + 1 and the cut turn ends "turn/end" interrupted
  When a new copy of A resumes
  Then it is refused naming B
  And seq runs 0..n with no gap, and A's lock file and hosts row are gone
  Cleanup: stop B; compare and remove the scratch home (evidence: home listing before and after)
  ```

#### Feature: Deletion leaves nothing (DH-E-STORE-6)

- **Scenario:** Shared images survive, cut deletions finish, nothing remains
  ```gherkin
  @TC-19s1 @DH-E-STORE-6 @proof:service:sqlite @venue:guarded @platform:linux-wsl @platform:windows
  Given a session S with two images and spill files, and a fork F that keeps the first image
  When S is deleted
  Then F and the shared image survive and the unreferenced image is collected
  Given a 10,000-event session L
  When its deletion is killed after the first chunk and the host restarts
  Then L is tombstoned and invisible, and after retention no row, blob or spill file of S or L remains
  And the integrity check reports clean
  Cleanup: remove the scratch home (evidence: deleted rows, blobs and spill files shown gone)
  ```

#### Feature: Two processes and an editor on one file (DH-E-FS-3)

- **Scenario:** Every conflicting write is refused stale and no edit is lost
  ```gherkin
  @TC-19s1 @DH-E-FS-3 @proof:service:git-and-editor @proof:service:os-process @venue:guarded @platform:linux-wsl @platform:windows
  Given two built dsh processes, each driving edit 1,000 times through the replay provider on one file in a scratch workspace
  And the editor save script installed on the lane machine (DSH_SCENARIO_FS3_EDITOR) saving the same file during the run
  When all three finish
  Then every conflicting write returned "fs/stale-version" and no edit was lost silently
  And the final file equals the last acknowledged version
  When one process is killed while it holds the file lock
  Then the other acquires it within the lock timeout
  Cleanup: stop both processes; remove the scratch home and workspace (evidence: file compared before and after)
  ```
- **Scenario:** A missing editor script fails preflight
  ```gherkin
  @TC-19s1 @DH-E-FS-3 @venue:guarded @edge
  Given a lane machine where DSH_SCENARIO_FS3_EDITOR is unset or names no executable
  When the guarded preflight runs
  Then it exits non-zero naming service:git-and-editor
  And the DH-E-FS-3 row reads "not run (git-and-editor preflight failed)" and S16 stays open
  ```

#### Feature: A crash in the middle of a sandboxed tool call (DH-E-STORE-2)

- **Scenario:** The next request after recovery carries the unknown outcome
  ```gherkin
  @TC-19s2 @DH-E-STORE-2 @proof:service:model-route-claude-code @proof:service:linux-sandbox-bwrap @venue:guarded @route:claude-code @platform:linux-wsl
  Given built dsh in a scratch home and workspace, workspace-write under bwrap Full
  And the signed-in Claude Code route [binding: replaces "a real model API key", M2-I-1]
  And before the kill the test snapshots every persisted event of the session (seq, type and payload)
  When DSH_SCENARIO_STORE2_KILL kills the host and its children while a sandboxed "sleep 60; write" runs
  And DSH_SCENARIO_STORE2_RESUME resumes the session and DSH_SCENARIO_STORE2_PROMPT sends a new prompt
  Then every pre-kill event is still present after resume with the same seq, type and payload, and seq continues with no gap
  And the cut turn shows interrupted and the tool call "outcome unknown"
  And the first model request read from the session log by DSH_SCENARIO_STORE2_REQUEST contains that result
  Cleanup: delete the session and show nothing remains; keep the home listing under DSH_LAB_EVIDENCE_URL; remove the workspace
  ```
- **Scenario:** Signed out or wrong backend is a failure, not a pass
  ```gherkin
  @TC-19s2 @venue:guarded @edge
  Given the WSL lane with CLAUDE_CONFIG_DIR pointed at an empty directory, or "dsh sandbox status" reporting "landlock Partial"
  When the guarded preflight runs
  Then it exits non-zero naming the failed check
  And the DH-E-STORE-2 row reads "not run (<check> preflight failed)" and DH-S-16 stays open
  ```

#### Feature: The guarded result is recorded, not inferred

- **Scenario:** Per-lane rows and S16 closure
  ```gherkin
  @TC-19s1 @TC-19s2 @proof:golden @venue:guarded
  Given the guarded runs on both lanes
  When the lanes write evidence/DH-S-16.md
  Then STORE-1, STORE-6 and FS-3 each have one row per lane, and STORE-2 has a WSL row and a Windows "out of scope" row
  And macOS rows read "out of scope (platforms-windows-wsl)"
  And "harness-gate ready --stage DH-S-16" exits 0 only when every in-scope row reads passed
  ```

# Phase 8: Acceptance on a Real Model

impl.md DH-S-17, implementation-plan Phase 7. TC-20s. Fourteen scenarios run on the Linux
E1 machine through the signed-in Claude Code route (must pass). The Copilot CLI route is
recorded where the specification asks for it (M2-DEC-4). Run order: TOOLCHAIN-1 first,
SANDBOX-7 last (contract rule 7). The milestone's user-visible promise is proved here:
`dsh headless "<task>"` and `dsh sdk` answering a real prompt.

### Setup
- **Type**: lab-bare-metal
- **Local**: on a Linux E1 machine (WSL is acceptable, M2-EN-1) with `claude`
  (and, for the recorded legs, `copilot`) signed in: `DSH_BIN=<built dsh> tools/harness-gate/lanes/wsl.sh --acceptance DH-S-17`.
  This runs `go test -tags scenario -p 1` over the 14 ids in schedule order, with
  `DSH_REQUIRE_SCENARIOS` set to them.
- **CI runner**: ADO pool `forge-lab-hw`, or GitHub self-hosted runner labels
  `[self-hosted, linux, dsh-e1, dsh-signed-in-claude, dsh-signed-in-copilot]`. Triggered by
  `harness-scenarios.yml` `workflow_dispatch` with `stage: DH-S-17`. The preflight job
  stays on `ubuntu-latest`.
- **Secrets**: no model key (M2-NG-2, M2-C-11). Machine access only:
  - Azure Key Vault `kv-forge-dsh-lab`, entry `dsh-lab-runner-token`, through ADO variable group `dsh-m2-lab`.
  - GitHub environment `dsh-m2-s17`, secret `DSH_LAB_RUNNER_TOKEN`.
  - Route inputs (`DSH_SCENARIO_CLAUDE_CODE_*`, `DSH_SCENARIO_COPILOT_CLI_*`) are environment variables.
  - DH-E-TOOLS-1's `DEEPSEEK_API_KEY` input is replaced by the route binding (M2-I-1), so no key is requested.
- **Pre-test bootstrap**: the s17 job's first steps are `harness-gate entry` and then the
  signed-in route preflight of `signedin_routes_test.go`, a probe turn per route. The
  lane script also checks:
  - `DSH_BIN` exists, and its checksum is recorded;
  - `claude` is signed in. If `copilot` is signed out, the copilot-cli legs read
    `failed (route preflight)` and are still kept;
  - `bwrap` is present for SDK-1;
  - `evidence/DH-S-16.md` reads closed (`harness-gate close --stage DH-S-16` exits 0).

  Any required miss fails the run.
- **Gate provisioning**: not applicable, because this phase is `lab-bare-metal`. These
  rows are acceptance only (`@venue:acceptance`) and never gate a merge. A missing
  service fails preflight, and the evidence row reads `not run`, never passed.

### Scenarios

#### Feature: Signed-in route runner and S17 workflow (Stage 7.1)

- **Scenario:** The workflow carries no key
  ```gherkin
  @TC-20s @proof:golden @venue:gate
  Given the committed .github/workflows/harness-scenarios.yml with its s17 job
  When the workflow lint in tools/harness-gate scans it
  Then no input, env or secret reference names a model key
  And the s17 job runs on workflow_dispatch only, on a self-hosted label, and equals tools/harness-gate/testdata/s17_job.golden (M2-NG-2, M2-C-11)
  ```
- **Scenario:** The required list holds exactly the 14 S17 ids
  ```gherkin
  @TC-20s @proof:golden @venue:gate
  Given the committed test/harness/acceptance-index.yaml
  When "harness-gate require --stage DH-S-17" runs
  Then it prints exactly the 14 ids of tech-spec sec. 4.2 rows 5-18, equal to the committed golden
  ```
- **Scenario:** A missing runner label leaves S17 not run
  ```gherkin
  @TC-20s @venue:acceptance @edge
  Given no online runner carries the dsh-e1 label
  When the s17 job is dispatched
  Then the job stays unscheduled and every S17 row reads "not run", never passed (M2-R-01)
  And evidence/runbook-s17-wsl.md names the signed-in WSL machine the record must cite when run by hand
  ```

#### Feature: Record and replay come first (DH-E-TOOLCHAIN-1)

- **Scenario:** A recording replays with no client and no network
  ```gherkin
  @TC-20s @DH-E-TOOLCHAIN-1 @proof:service:model-route-claude-code @venue:acceptance @route:claude-code
  Given the scenario directory DSH_SCENARIO_TOOLCHAIN1_DIR and the signed-in route
  When DSH_SCENARIO_TOOLCHAIN1_RECORD records a run
  And DSH_SCENARIO_TOOLCHAIN1_REPLAY replays it twice with the route disabled, empty config homes, stand-in clients that fail if started, and a proxy that refuses every connection
  Then the replayed persisted log, workspace and requests equal the recording after normalization and no client process starts
  And the TOOLCHAIN-1 row of evidence/DH-S-17.md carries binary checksum, model id, run id and commit (M2-DEC-5)
  Cleanup: remove the temporary homes and the scratch store
  ```

#### Feature: Sessions on a real model (DH-E-SESSION-1, DH-E-SESSION-2)

- **Scenario:** A session rebuilds from its log
  ```gherkin
  @TC-20s @DH-E-SESSION-1 @proof:service:model-route-claude-code @venue:acceptance @route:claude-code
  Given a scratch home and a session with three real turns
  When the views are dropped and rebuilt (TestRequestsReceivedEqualRequestsRebuilt)
  Then every view equals its pre-drop value and the requests received equal the requests rebuilt
  Cleanup: remove the scratch home
  ```
- **Scenario:** A killed host resumes the session
  ```gherkin
  @TC-20s @DH-E-SESSION-2 @proof:service:model-route-claude-code @venue:acceptance @route:claude-code @edge
  Given a real turn in progress during a tool call (TestKilledDuringToolCallResumesIntoAcceptedHistory)
  When the host is killed and restarted
  Then the session resumes, the cut turn reads interrupted, the next turn completes, and the persisted log has no seq gap
  Cleanup: confirm no dsh process remains; remove the scratch home
  ```

#### Feature: The agent loop on a real model (DH-E-AGENT-1..4)

- **Scenario:** One turn with tools completes (AGENT-1)
  ```gherkin
  @TC-20s @DH-E-AGENT-1 @venue:acceptance @route:claude-code
  Given DSH_SCENARIO_AGENT1_LAYER and DSH_SCENARIO_AGENT1_PROMPT
  When the turn runs
  Then it ends completed with every tool call logged and replayable
  ```
- **Scenario:** Cancel mid-turn (AGENT-2)
  ```gherkin
  @TC-20s @DH-E-AGENT-2 @venue:acceptance @route:claude-code @edge
  Given a long real turn
  When the client cancels
  Then "turn/end" reads cancelled and no tool child remains
  ```
- **Scenario:** Stop then kill (AGENT-3)
  ```gherkin
  @TC-20s @DH-E-AGENT-3 @venue:acceptance @route:claude-code @edge
  Given a turn running a tool that ignores the stop
  When stop is requested and the grace period ends
  Then the tool is killed and the turn ends with the stop reason logged
  ```
- **Scenario:** The step budget ends the turn (AGENT-4)
  ```gherkin
  @TC-20s @DH-E-AGENT-4 @venue:acceptance @route:claude-code @edge
  Given a layer with "budget.maxSteps: 2" and a prompt needing more steps
  When the turn runs
  Then it ends after two steps with the budget reason
  And each of the four AGENT records cites its prompt count (M2-C-5)
  Cleanup: confirm no tool child remains; remove the scratch homes
  ```

#### Feature: Model routes (DH-E-LLM-1, DH-E-LLM-3)

- **Scenario:** A real turn on each route (LLM-1)
  ```gherkin
  @TC-20s @DH-E-LLM-1 @venue:acceptance @route:claude-code @route:copilot-cli
  Given both signed-in routes
  When one turn runs on each
  Then the claude-code leg must pass and the copilot-cli leg's result is recorded beside it
  ```
- **Scenario:** Signed out is permanent and has no fallback, claude-code leg (LLM-3, must pass)
  ```gherkin
  @TC-20s @DH-E-LLM-3 @proof:service:model-route-claude-code @venue:acceptance @route:claude-code @edge
  Given TestSignedOutIsAFact subtest claude-code with CLAUDE_CONFIG_DIR pointed at an empty temporary directory
  When a turn starts
  Then the error is "llm/sign-in-required", classified Permanent
  And retries end with "llm/retry-abandoned" and no other route is tried
  When a request sets MaxTokens on a route that cannot honour it
  Then it is refused "llm/unsupported-option"
  Cleanup: remove the temporary config directory
  ```
- **Scenario:** Signed out is permanent and has no fallback, copilot-cli leg (LLM-3, recorded)
  ```gherkin
  @TC-20s @DH-E-LLM-3 @proof:service:model-route-copilot @venue:acceptance @route:copilot-cli @edge
  Given TestSignedOutIsAFact subtest copilot-cli with COPILOT_HOME pointed at an empty temporary directory
  When a turn starts
  Then the same Then clauses are evaluated and the result, passed or failed, is kept as the copilot-cli row of LLM-3 (M2-DEC-4)
  Cleanup: remove the temporary COPILOT_HOME
  ```

#### Feature: Title, tools, loader (DH-E-VIEW-1, DH-E-TOOLS-1, DH-E-LOADER-1)

- **Scenario:** A session gets a title (VIEW-1)
  ```gherkin
  @TC-20s @DH-E-VIEW-1 @venue:acceptance @route:claude-code @route:copilot-cli
  Given DSH_SCENARIO_VIEW1_LAYER and DSH_SCENARIO_VIEW1_PROMPT
  When the first turn ends
  Then a title is set, the claude-code leg must pass and the copilot-cli leg is recorded
  And with DSH_SCENARIO_VIEW1_BAD_LAYER the title route failure is logged and the turn still completes
  ```
- **Scenario:** The tool pipeline asks before writing (TOOLS-1)
  ```gherkin
  @TC-20s @DH-E-TOOLS-1 @venue:acceptance @route:claude-code
  Given DSH_SCENARIO_TOOLS1_LAYER asking before scenario_write [binding: signed-in route replaces deepseek-official, M2-I-1]
  When DSH_SCENARIO_TOOLS1_PROMPT runs and the approval is granted
  Then c.txt is written and DSH_SCENARIO_TOOLS1_REPLAY rebuilds identical requests
  ```
- **Scenario:** A live edit is applied without restart (LOADER-1)
  ```gherkin
  @TC-20s @DH-E-LOADER-1 @venue:acceptance @edge
  Given the harness running DSH_SCENARIO_LOADER_PROFILE
  When the layer is edited
  Then the change is applied within 2 s with the pid unchanged and the log shows it taking effect on the next turn
  And an invalid YAML edit is reported with file:line:col while the old config stays active
  And DSH_SCENARIO_LOADER_STOP exits 0 with no temp file left
  Cleanup: remove the scratch home and layer directory
  ```

#### Feature: The SDK on a real model (DH-E-SDK-1)

- **Scenario:** A Go program drives dsh sdk through a full session
  ```gherkin
  @TC-20s @DH-E-SDK-1 @proof:service:model-route-claude-code @venue:acceptance @route:claude-code
  Given a Go program on src/sdk/sdk, process TZ=UTC, initialize sending timeZone "Asia/Tokyo"
  When it runs DSH_SCENARIO_SDK1_LAYER with a prompt
  Then the clock context is in Asia/Tokyo and the run result equals the last assistant message
  When it rejects a write
  Then the file is unchanged
  When on the same connection the model asks a question (question/ask) and the program answers with "blue"
  Then the answer is accepted, the turn continues, and the next assistant message or tool input in the persisted history contains "blue"
  And an answer sent for an already-answered question id is rejected with an sdk/* error
  When it cancels DSH_SCENARIO_SDK1_LONG_PROMPT
  Then the cancel reply arrives after "turn/end"
  When it kills dsh
  Then the client sees EOF and the exit status
  And a resume reports closedInterruptedTurn with no seq gap
  And stdout held frames only (M2-C-10), and no dsh process remains
  And session.run.completed text equals the last assistant message in the history read back from the scratch store
  Cleanup: remove the scratch home
  ```

#### Feature: Policy text proved last (DH-E-SANDBOX-7)

- **Scenario:** The platform texts make the model escalate
  ```gherkin
  @TC-20s @DH-E-SANDBOX-7 @proof:service:model-route-claude-code @venue:acceptance @route:claude-code
  Given the binary built from the unmerged branch s17/policy-text head carrying world/sandbox/policy/text/{linux,darwin,windows}.md and policy_section_{linux,darwin,windows}.golden
  And the s17 job dispatched with ref s17/policy-text, last in S17 (R-03)
  When 12 tasks from DSH_SCENARIO_SANDBOX7_TASKS x 3 runs x 3 platform texts read from the resolver (108 runs) complete in one budgeted job (M2-C-5)
  Then every run made at least one tool call
  And for each text at least 10 of its 18 denied-first tasks escalated, and the transcripts are kept
  And the row records the branch commit, run id and SHA-256 of each text file, closing UP-DEBT-found.Q3.2
  Cleanup: remove the scratch homes; on failure the branch is not merged and no shipped text exists on main
  ```
- **Scenario:** The text is bound at merge
  ```gherkin
  @TC-20s @TC-01g @DH-E-SANDBOX-7 @proof:golden @venue:gate
  Given the merged world/sandbox/policy/text/*.md, its policy_section_*.golden and the SANDBOX-7 row of evidence/DH-S-17.md
  When "harness-gate policy-text" runs
  Then each file's SHA-256 equals the row's value for its platform and each golden equals the render of that text (M2-C-6)
  ```
- **Scenario:** Text edited after the pass reopens the proof
  ```gherkin
  @TC-20s @DH-E-SANDBOX-7 @venue:gate @edge
  Given the passing record and a later edit to a text file
  When "harness-gate policy-text" runs
  Then it fails naming DH-E-SANDBOX-7 until a new run passes on the new version
  ```

#### Feature: S17 closure record (Stage 7.7)

- **Scenario:** S17 ready, then closed
  ```gherkin
  @TC-20s @proof:golden @venue:gate
  Given evidence/DH-S-17.md reading "Closure status: open" with all 14 rows (runner label or named WSL machine, commit, binary checksum, model id, result, artifacts)
  When "harness-gate ready --stage DH-S-17" runs, and after the status change "harness-gate close --stage DH-S-17" runs
  Then both exit 0
  And the fixture testdata/closure/closed_not_ready/ makes close exit non-zero with "closed-but-not-ready" (M2-C-2)
  ```
- **Scenario:** The Copilot leg is kept
  ```gherkin
  @TC-20s @proof:golden @venue:gate @edge
  Given the committed S17 record
  When the close check reads DH-E-LLM-1, DH-E-LLM-3 and DH-E-VIEW-1
  Then each has a copilot-cli result row, passed or failed, never missing (M2-DEC-4)
  ```

# Phase 9: Acceptance on Real Operating Systems

impl.md DH-S-18, implementation-plan Phase 8. TC-04s, TC-07s, TC-21s1..TC-21s5. Nineteen
scenarios run on team-kept machines: Linux E2 variants, a Windows host, file shares and
two power-cut VMs (M2-EN-3..8). The run order is TOOLCHAIN-2 first and NODEFS-1 last.
Each variant is recorded `passed`, `failed`, `not run (<reason>)` or `out of scope (<decision>)`.

### Setup
- **Type**: lab-bare-metal
- **Local**: a team member with SSH access to the lab machines runs
  `DSH_BIN=<built dsh> tools/harness-gate/lanes/wsl.sh --acceptance DH-S-18` for the
  Linux variants. Windows variants run with `tools/harness-gate/lanes/win.ps1 -Acceptance DH-S-18`
  on the Windows host. Each lane runs `go test -tags scenario -p 1` over its ids.
- **CI runner**: ADO pool `forge-lab-hw`, or GitHub self-hosted runners with these labels:
  - `[self-hosted, linux, dsh-e2-controller]`: drives the E2 VMs over SSH;
  - `[self-hosted, windows, dsh-windows-host]`;
  - `[self-hosted, linux, dsh-hypervisor]`: KVM and Hyper-V power-cut control.

  Triggered by `harness-scenarios.yml` `workflow_dispatch` with `stage: DH-S-18`.
- **Secrets**: no model key. The signed-in routes live on the machine that runs the
  scenario, never on the controller: VIEW-5 and the Linux FS-2 leg use the tester's
  Linux machine; the Windows FS-2 leg uses a natively installed, signed-in Claude Code
  CLI on the Windows variant machine (`[self-hosted, windows, dsh-windows-host]`).
  Machine access only:
  - Azure Key Vault `kv-forge-dsh-lab` entries `dsh-lab-ssh-key`, `dsh-lab-known-hosts`,
    `dsh-lab-hyperv-cred`, `dsh-lab-second-user-cred` and `dsh-lab-runner-token`, exposed
    through ADO variable group `dsh-m2-lab`.
  - GitHub environment `dsh-m2-s18` with secrets `DSH_LAB_SSH_KEY`,
    `DSH_LAB_KNOWN_HOSTS`, `DSH_LAB_HYPERV_CRED`, `DSH_LAB_SECOND_USER_CRED` and
    `DSH_LAB_RUNNER_TOKEN`.
  - Machine identities are referenced only through `DSH_SCENARIO_SANDBOX1_MACHINES`,
    `DSH_SCENARIO_EXEC10_SSH`, `DSH_SCENARIO_VMS` and similar env vars.
- **Pre-test bootstrap**: each `s18` variant job (Stage 8.1) first runs
  `harness-gate entry`. It then runs the service preflight named for its variant in
  `evidence/variants.md`, and finally takes `DSH_REQUIRE_SCENARIOS` from
  `harness-gate require --stage DH-S-18`, filtered to that variant. The preflights:

  | Service | Preflight |
  |---|---|
  | `service:linux-sandbox-bwrap` | `bwrap Full` |
  | `service:linux-landlock-only` | `landlock` |
  | `service:linux-unconfined` | `Unconfined` |
  | `service:linux-systemd-cgroup` | `systemctl --user` state recorded |
  | Share variants | Share mounts checked |
  | STORAGE-6 machine | Second account checked |
  | `service:vm-power-cut` | Creates, cuts and deletes a throw-away guest |
  | `service:ack-recorder` | Listening on `DSH_SCENARIO_NODEFS_LISTEN` on the hypervisor host |

  A failed check marks only that variant `not run (<check> preflight failed)`, and the
  stage stays open (M2-C-2).
- **Gate provisioning**: not applicable, because this phase is `lab-bare-metal`. No row
  enters a gate required list. A missing machine is a preflight failure recorded as *not run*, never a pass.

### Scenarios

#### Feature: E2 variant fleet and runbooks (Stage 8.1)

- **Scenario:** The variant list covers the index
  ```gherkin
  @TC-21s1 @proof:golden @venue:gate
  Given evidence/variants.md and the DH-S-18 entry of test/harness/acceptance-index.yaml
  When the index check cross-references them
  Then every S18 scenario has at least one in-scope variant or an owner decision removing it (M2-DOD-4)
  And macOS rows read "out of scope (platforms-windows-wsl)"
  ```
- **Scenario:** A preflight refuses the wrong machine
  ```gherkin
  @TC-21s1 @proof:in-process @venue:gate @edge
  Given recorded "dsh sandbox status" outputs
  When the service:linux-landlock-only preflight evaluates a "bwrap Full" output, and the service:linux-unconfined preflight evaluates a "landlock" output
  Then both fail naming the expected state
  ```

#### Feature: The release rehearsal comes first (DH-E-TOOLCHAIN-2)

- **Scenario:** Six archives, two executed systems, keyless replay
  ```gherkin
  @TC-21s3 @DH-E-TOOLCHAIN-2 @proof:service:go-toolchain @venue:acceptance @platform:linux-wsl @platform:windows
  Given a clean checkout at the recorded commit
  When the rehearsal builds the release
  Then six archives and one checksum file exist
  And on Linux and on Windows the unpacked "dsh --version" exits 0 and prints the embedded version matching the record, with no "modified" marker
  And the keyless headless replay passes, and every later S18 step uses these binaries whose checksums are in evidence/DH-S-18.md
  Cleanup: remove the unpacked archives from each variant
  ```

#### Feature: Linux confinement on real kernels (DH-E-SANDBOX-1, DH-E-NATIVESYSTEM-1..4)

- **Scenario:** bwrap machine reports bwrap Full (SANDBOX-1 a)
  ```gherkin
  @TC-21s1 @TC-04s @DH-E-SANDBOX-1 @proof:service:linux-sandbox-bwrap @venue:acceptance @platform:linux-e2:bwrap
  Given the bwrap machine of DSH_SCENARIO_SANDBOX1_MACHINES (preflight "bwrap Full")
  When a workspace-write command runs
  Then writes inside the roots succeed, writes outside are denied, and "dsh sandbox status" reports bwrap Full
  And exit codes 0, 1, 2 and 125 and the stderr texts "bwrap: fatal" and "dsh sandbox-launch: failed" pass through unchanged
  And the reported enforcement is copied into evidence/DH-S-18.md
  Cleanup: remove the scratch home and workspace over SSH
  ```
- **Scenario:** Landlock-only machine reports landlock with missing effects named (SANDBOX-1 b)
  ```gherkin
  @TC-21s1 @TC-04s @DH-E-SANDBOX-1 @proof:service:linux-landlock-only @venue:acceptance @platform:linux-e2:landlock
  Given the Landlock-only machine (no bwrap, Landlock ABI >= 5; preflight "landlock")
  When the same command runs
  Then confinement holds and status reports landlock, Full or Partial, with each missing effect named
  And the reported enforcement is copied into evidence/DH-S-18.md
  Cleanup: remove the scratch home and workspace over SSH
  ```
- **Scenario:** Machine with neither backend reports Unconfined and refuses (SANDBOX-1 c)
  ```gherkin
  @TC-21s1 @TC-04s @DH-E-SANDBOX-1 @proof:service:linux-unconfined @venue:acceptance @platform:linux-e2:unconfined @edge
  Given the machine with neither bwrap nor Landlock (preflight "Unconfined")
  When a workspace-write command is started with "require: full"
  Then it is refused "sandbox/unavailable" or "sandbox/enforcement-insufficient" and no command runs (marker file absent)
  And "dsh sandbox status" reports Unconfined, copied into evidence/DH-S-18.md
  Cleanup: remove the scratch home over SSH
  ```
- **Scenario:** A launcher placed in a writable root is refused
  ```gherkin
  @TC-21s1 @DH-E-SANDBOX-1 @venue:acceptance @edge
  Given a dsh binary copied into the write root W
  When it starts a confined command
  Then the start is refused "sandbox/launcher-writable"
  ```
- **Scenario:** Landlock ABI >= 5, pure-Go build (NATIVESYSTEM-1)
  ```gherkin
  @TC-21s1 @DH-E-NATIVESYSTEM-1 @venue:acceptance @platform:linux-e2:landlock
  Given a CGO_ENABLED=0 build on a kernel with Landlock ABI >= 5
  When a confined command and a setuid helper run
  Then the record reads "confined, landlock, full"
  And the pid, environment and signal checks pass
  ```
- **Scenario:** ABI 1-4 reports Partial (NATIVESYSTEM-2, separate old-kernel Landlock variant)
  ```gherkin
  @TC-21s1 @DH-E-NATIVESYSTEM-2 @proof:service:linux-landlock-only @venue:acceptance @platform:linux-e2:landlock-abi1-4 @edge
  Given a Landlock variant (not the SANDBOX-1 unconfined machine) whose kernel has Landlock ABI between 1 and 4
  When a command fails with its own exit code
  Then enforcement reads Partial and the child's result is not reported as a launcher failure
  ```
- **Scenario:** No Landlock, or a missing grant path, refuses before exec (NATIVESYSTEM-3)
  ```gherkin
  @TC-21s1 @DH-E-NATIVESYSTEM-3 @venue:acceptance @edge
  Given Landlock disabled, and separately a grant path that does not exist
  When the stage prepares
  Then it refuses and marker M is absent
  ```
- **Scenario:** cgo+race and musl builds behave the same (NATIVESYSTEM-4)
  ```gherkin
  @TC-21s1 @DH-E-NATIVESYSTEM-4 @venue:acceptance @platform:linux-e2:musl
  Given a cgo -race build and a pure-Go build, and a musl system
  When NATIVESYSTEM-1's checks run on each
  Then the records are equal
  ```

#### Feature: Process trees and scopes (DH-E-EXEC-1, DH-E-EXEC-10)

- **Scenario:** A grandchild that ignores SIGTERM is ended (EXEC-1)
  ```gherkin
  @TC-21s2 @DH-E-EXEC-1 @proof:service:linux-systemd-cgroup @venue:acceptance @platform:linux-e2:systemd
  Given a setsid grandchild that ignores SIGTERM, run with DSH_SCENARIO_EXEC1_RUN
  When the 2 s timeout fires
  Then the record reads "stop: timeout" and "owner: cgroup", and the cgroup directory is removed
  When dsh is killed with SIGKILL mid-run
  Then the next start lists the survivors
  And on DSH_SCENARIO_EXEC1_NOCGROUP the record reads "owner: process-group" with exactly one warning
  ```
- **Scenario:** A systemd scope with limits over SSH (EXEC-10)
  ```gherkin
  @TC-21s2 @DH-E-EXEC-10 @venue:acceptance @platform:linux-e2:systemd
  Given DSH_SCENARIO_EXEC10_SSH
  When dsh starts
  Then it makes one scope request
  And a 256 MiB limit records "limits: enforced"
  And the control socket refuses a connection from inside the tree with "PeerTree Exact"
  And on DSH_SCENARIO_EXEC10_SSH_NOMANAGER the record reads "owner: process-group" and "limits: unavailable", and a survivor is reported
  Cleanup: kill any reported survivor and remove the scratch account's home over SSH
  ```

#### Feature: The fence and long sessions on real filesystems (DH-E-FS-1, DH-E-FS-2, DH-E-VIEW-5)

- **Scenario:** 10,000 writes under a link swap (FS-1)
  ```gherkin
  @TC-21s3 @DH-E-FS-1 @venue:acceptance @platform:linux-wsl @platform:windows
  Given checkouts on Linux ext4, Windows NTFS and DSH_SCENARIO_FS1_SHARE (SMB)
  When 10,000 write, edit and apply_patch calls run while a helper swaps links
  Then the outside hash listing is unchanged
  And every refusal is "fs/sandbox-denied" with no staged file
  And a write in read-only mode is refused
  And an escalated danger-full-access write succeeds and is logged
  Cleanup: on any outside write stop the stage and keep both listings (M2-R-03); otherwise remove the checkouts
  ```
- **Scenario:** A long session corrects its own bad patterns, Linux leg (FS-2)
  ```gherkin
  @TC-21s4 @DH-E-FS-2 @proof:service:model-route-claude-code @proof:service:git-and-editor @venue:acceptance @route:claude-code @platform:linux-wsl
  Given the tester's Linux machine (ext4) whose s18 job preflight confirmed a signed-in Claude Code CLI (probe turn), git and the editor save script
  And 20 tasks over DSH_SCENARIO_FS_CHECKOUTS on that machine's signed-in route
  When they run (sharded per M2-R-06 if beyond 3 h)
  Then fewer than 5% of grep calls return "search/invalid-pattern", and each one is followed by a corrected call
  And no rg child is started
  And the disk equals the logged edits, and the before/after listings show zero files outside the write roots
  Cleanup: keep both listings as artifacts; remove the scratch homes and checkouts
  ```
- **Scenario:** A long session corrects its own bad patterns, Windows leg (FS-2)
  ```gherkin
  @TC-21s4 @DH-E-FS-2 @proof:service:windows-host @proof:service:model-route-claude-code @proof:service:git-and-editor @venue:acceptance @route:claude-code @platform:windows
  Given the Windows variant machine (NTFS) from evidence/variants.md
  And on that machine a natively installed Claude Code CLI signed in by the tester, git and the editor save script, each confirmed by the Windows s18 job preflight
  And the mode is workspace-write with sandbox status Unconfined (no Windows backend in M2), so the fence under test is the C32 filesystem seam, recorded in the row
  When the same 20 tasks run on that machine's signed-in route (sharded per M2-R-06 if beyond 3 h)
  Then the same Then clauses hold and no file appears outside the write roots in the before/after listings
  Cleanup: keep both listings as artifacts; remove the scratch homes and checkouts
  ```
- **Scenario:** A Windows machine without its own signed-in client is not run
  ```gherkin
  @TC-21s4 @DH-E-FS-2 @venue:acceptance @platform:windows @edge
  Given the Windows variant machine where claude is missing or signed out, or git or the editor save script is absent
  When the Windows s18 job preflight runs
  Then it fails naming the missing dependency, and the Windows FS-2 row reads "not run (<check> preflight failed)"
  And a signed-in client on the controller or the Linux machine does not satisfy it, and S18 stays open
  ```
- **Scenario:** The working directory on a real repository (VIEW-5)
  ```gherkin
  @TC-21s4 @DH-E-VIEW-5 @proof:service:model-route-claude-code @proof:service:linux-sandbox-bwrap @venue:acceptance @route:claude-code @platform:linux-wsl
  Given a session under workspace-write on a git repository with a worktree, "dsh sandbox status" reading bwrap Full [binding: the UI directory-picker clause waits for the UI stage]
  When no tool changes the directory and the host moves the session to the worktree
  Then exactly one "workspace-root/change" is emitted
  And the write lands in the worktree, and a write to the original project is denied
  When the worktree is deleted
  Then the session recovers
  When the repository is deleted
  Then the turn is rejected "workdir/unavailable"
  Cleanup: remove the scratch repository and home
  ```

#### Feature: Search and watch on real machines (DH-E-RIPGREP-1, -2, DH-E-CHOKIDAR-1, -2)

- **Scenario:** Search within budget (RIPGREP-1)
  ```gherkin
  @TC-21s3 @DH-E-RIPGREP-1 @venue:acceptance @platform:linux-wsl @platform:windows
  Given a tree of 100k files and DSH_SCENARIO_SEARCH_BUDGET
  When grep runs
  Then the first 250 matches arrive within 3 s
  And the time ratio against rg is recorded with the agreed budget
  ```
- **Scenario:** The listing equals git's (RIPGREP-2)
  ```gherkin
  @TC-21s3 @DH-E-RIPGREP-2 @venue:acceptance @edge
  Given a repository with ignored and untracked files
  When glob lists all files
  Then the list equals "git ls-files --cached --others --exclude-standard" minus hidden files
  ```
- **Scenario:** Real editor saves are each seen once (CHOKIDAR-1)
  ```gherkin
  @TC-21s3 @DH-E-CHOKIDAR-1 @proof:service:git-and-editor @venue:acceptance
  Given saves by rename, in place, and backup-then-write
  When each happens
  Then each is reported exactly once within 1 s
  ```
- **Scenario:** Network and cross-OS directories poll (CHOKIDAR-2)
  ```gherkin
  @TC-21s3 @DH-E-CHOKIDAR-2 @venue:acceptance @edge
  Given DSH_SCENARIO_WATCH_SHARES (SMB, NFS) and a Windows directory seen from WSL
  When a file changes
  Then the watcher reports Poll mode and sees the change within two poll intervals
  ```

#### Feature: Instruction files and private storage (DH-E-CONTEXT-2, DH-E-STORAGE-6, DH-E-HOST-4)

- **Scenario:** Editor saves and branch switches update the instructions (CONTEXT-2)
  ```gherkin
  @TC-07s @TC-21s4 @DH-E-CONTEXT-2 @proof:service:git-and-editor @venue:acceptance @platform:linux-wsl @platform:windows
  Given a session on a repository with AGENTS.md
  When the file is saved by an editor
  Then exactly one "updated" message is produced
  And the next model request contains the edited instruction section and no longer the replaced text
  When separately a git branch switch changes AGENTS.md
  Then exactly one "updated" message is produced
  And the next model request contains the switched branch's instruction section
  And a linked sub/AGENTS.md pointing outside the root is refused and named in the diagnostics
  And no turn fails
  Cleanup: remove the scratch repository and home
  ```
- **Scenario:** A second account cannot read (STORAGE-6)
  ```gherkin
  @TC-21s3 @DH-E-STORAGE-6 @venue:acceptance @platform:linux-wsl @platform:windows
  Given DSH_SCENARIO_STORAGE6_USER and data seeded with DSH_SCENARIO_STORAGE6_SEED
  When that user lists and reads blobs, spill and cache
  Then every request is denied
  When the spill directory is made group-writable
  Then the spill component refuses to start, naming the directory, while the rest of the host runs
  Cleanup: remove the seeded data and restore the spill directory mode; the second account is kept per the runbook
  ```
- **Scenario:** Crash reports and safe mode (HOST-4)
  ```gherkin
  @TC-21s3 @DH-E-HOST-4 @venue:acceptance @platform:linux-wsl @platform:windows @edge
  Given a host started with DSH_SCENARIO_HOST4_ARGS on a fresh short home and answering DSH_SCENARIO_HOST4_READY
  When it is killed with no grace (SIGKILL on Linux, TerminateProcess on Windows) and started again
  Then the next start prints the unclean-exit line naming an owner-only report under <home>/reports and "--safe"
  Given DSH_SCENARIO_HOST4_LAYER with failure set to load, panic and fault in turn
  When the host starts each time
  Then each run exits non-zero and stderr names a report readable only by the owner
  And the next start prints the unclean-exit line and "--safe"
  And DSH_SCENARIO_HOST4_READY succeeds once the listener answers [binding: M2-DEC-6 replaces "reaches the UI"]
  And after 12 failures 10 reports remain
  Cleanup: remove the scratch home and its crash reports
  ```

#### Feature: Power cuts come last (DH-E-NODEFS-1)

- **Scenario:** No acknowledged write is lost
  ```gherkin
  @TC-21s5 @DH-E-NODEFS-1 @proof:service:vm-power-cut @proof:service:ack-recorder @venue:acceptance
  Given for each guest OS in DSH_SCENARIO_VMS (Linux on KVM/libvirt, Windows on Hyper-V) a disposable copy-on-write overlay created fresh for this run from the team-kept base image
  And the ack recorder running on a second machine, the hypervisor host, listening on DSH_SCENARIO_NODEFS_LISTEN and isolated from the guest's disk
  And TestDurableReplaceSurvivesPowerLoss running fsx.Replace loops in the guest, sending each acknowledged version to the recorder
  When the guest is hard powered off 100 times, each after a random delay ("virsh destroy" on KVM, "Stop-VM -TurnOff" on Hyper-V via DSH_SCENARIO_NODEFS_POWEROFF), and restarted with DSH_SCENARIO_NODEFS_POWERON
  Then after every restart the file read through the guest agent is complete and never older than the recorder's last acknowledged version
  And runs beyond the 3 h variant limit are sharded (M2-R-06), NODEFS-1 running last in S18
  Cleanup: delete each passing overlay and the guest definition and disk; keep the recorder log under DSH_SCENARIO_REPORT; on failure keep the failing overlay and name it in evidence/DH-S-18.md
  ```
- **Scenario:** Unavailable variants keep the stage open
  ```gherkin
  @TC-21s5 @venue:acceptance @edge
  Given the Hyper-V host failing preflight
  When the stage record is written
  Then the Windows guest row reads "not run (hyperv preflight failed)"
  And "harness-gate ready --stage DH-S-18" fails naming it
  ```

#### Feature: S18 closure record (Stage 8.10)

- **Scenario:** No partial closure
  ```gherkin
  @TC-21s2 @proof:in-process @venue:gate @edge
  Given a record fixture where one in-scope variant of DH-E-EXEC-10 reads "not run"
  When "harness-gate ready --stage DH-S-18" runs on it
  Then S18 is refused naming that variant (M2-DOD-4, M2-C-2)
  ```
- **Scenario:** S18 ready, then closed
  ```gherkin
  @proof:golden @venue:gate
  Given evidence/DH-S-18.md with all 19 scenarios passed on every in-scope variant, enforcement copied, and out-of-scope variants naming their deciding decision
  When "harness-gate ready --stage DH-S-18" runs while open, and "harness-gate close --stage DH-S-18" runs after the status change
  Then both exit 0 and evidence/closure.md is updated for the deliverables S18 finishes
  ```

---

# Phase 10: M2 Milestone Closure

implementation-plan Phase 9, the M2 row of impl.md sec. 3.3. Closure is proved over the
records only: no scenario reruns, nothing is published (M2-NG-*).

### Setup
- **Type**: inline
- **Local**: `GOWORK=off go -C tools/harness-gate test ./...`, which runs the closure and
  milestone predicate fixtures and the release check. After that, run
  `GOWORK=off go -C tools/harness-gate run . close --stage DH-S-18 --root ../..` against
  the committed records.
- **CI runner**: GitHub-hosted `ubuntu-latest` and `windows-latest`, `harness` job of `ci.yml`.
- **Secrets**: none.
- **Pre-test bootstrap**: none.
- **Gate provisioning**: no connection env-var. The predicate reads committed records,
  or fixture trees copied into `t.TempDir()`. No service is needed, and nothing skips.

### Scenarios

#### Feature: Deliverable closure record (Stage 9.1)

- **Scenario:** Done needs every proof
  ```gherkin
  @proof:in-process @venue:gate @edge
  Given an in-memory closure table marking CLI-2 done while DH-E-CLI-1 is unrun
  When the closure predicate runs
  Then it refuses naming CLI-2 and DH-E-CLI-1 (M2-C-3)
  ```
- **Scenario:** Counts match the tech spec
  ```gherkin
  @proof:golden @venue:gate
  Given the committed docs/stories/harness-DSH-M2/evidence/closure.md
  When the closure check counts rows
  Then there are 35 deliverables, at most the 11 done-candidates read done, every built row names a finishing stage or H-item, and H-21 reads closed (M2-I-5, M2-DOD-6)
  ```

#### Feature: Milestone M2 declaration and enabling check (Stage 9.2)

- **Scenario:** Milestone predicate
  ```gherkin
  @proof:in-process @venue:gate @edge
  Given fixture records with DH-S-17 closed and DH-S-18 open
  When the milestone check runs
  Then M2 is refused naming DH-S-18
  And with both closed and the step 16.8 guarded rows passed it is accepted and built-through advances per the index rules
  ```
- **Scenario:** Nothing is published
  ```gherkin
  @proof:golden @venue:gate
  Given .github/workflows/release.yml, .goreleaser.yaml and the default profile bundles at the closing commit
  When the release check compares them with tools/harness-gate/testdata/release_base.golden and computes the default egress set
  Then the release files are unchanged, no dsh release job is added, and the egress set is empty (M2-DOD-7, NG-7)
  ```

