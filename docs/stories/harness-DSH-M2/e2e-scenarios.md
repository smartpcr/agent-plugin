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
| 6 Profiles, Launchers, Evidence Runners and Keyless Guarded Runs | Phase 6 (Stages 6.1-6.11, 6.12 for STORE-1/-6, FS-3) | DH-S-16, step 16.8 | gate + guarded | inline |
| 7 Guarded Recovery Under the Linux Sandbox | Phase 6 (Stage 6.12, STORE-2 only) | DH-S-16, step 16.8 | guarded | lab-bare-metal |
| 8 Acceptance on a Real Model | Phase 7 | DH-S-17 | acceptance | lab-bare-metal |
| 9 Acceptance on Real Operating Systems | Phase 8 | DH-S-18 | acceptance | lab-bare-metal |
| 10 M2 Milestone Closure | Phase 9 | sec. 3.3 M2 row | gate | inline |

Step 16.8 is split across Phases 6 and 7 for one reason only: DH-E-STORE-1, -6 and
FS-3 need nothing but the built binary, a scratch home and the keyless replay provider
(they self-provision), while DH-E-STORE-2 needs the tester's signed-in Claude Code
client and `bwrap` on the WSL lane machine (TC-19s2), which no test can create. Both
phases still finish before any Phase 8 or 9 run (contract rule 7), and S16 closes only
when all four passed.

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
- **Built binary once.** Gate and guarded scenarios that run `dsh` build it once per
  test binary through `testkit/dshbin` (over the M1 `testkit/launcher.Build`);
  acceptance scenarios receive it through `DSH_BIN`, pointing at the binary whose
  checksum the evidence record cites (F7 "build once").

### Env-var inputs

"Existing" means the name is already read by a file under
`src/harness/internal/testkit/scenarios/`; "new" names are introduced by the scenario
files this story authors and follow the same `need(t, ...)` pattern (a missing input
fails the scenario naming the variable; it never skips).

| Env var | Kind | Used by | When unset |
|---|---|---|---|
| `DSH_BIN` | path to built `dsh` | C53, C55, every guarded and acceptance scenario | Gate and guarded: `testkit/dshbin` builds it into `t.TempDir()`. Acceptance: required, the run fails |
| `DSH_REQUIRE_SCENARIOS` | comma list | C55, `harness-scenarios.yml` preflight | Gate: empty, nothing required. Guarded/acceptance: derived by `harness-gate require --stage <id>` |
| `DSH_SCENARIO_REPORT` | file path | C55 evidence statement; NODEFS-1 version log | Scenario writes its statement to `t.TempDir()` and logs the path |
| `DSH_HOME` | path | dsh by hand only | Always removed from children by the test kit |
| `DSH_SCENARIO_CLAUDE_CODE_MODEL`, `DSH_SCENARIO_CLAUDE_CODE_EFFORT` | route inputs (existing, `signedin_routes_test.go`) | Phases 7, 8, FS-2, VIEW-5 | Required on lab phases; run fails |
| `DSH_SCENARIO_COPILOT_CLI_MODEL`, `DSH_SCENARIO_COPILOT_CLI_EFFORT` | route inputs (existing) | Phase 8 recorded legs | Recorded leg reads `failed (input missing)`; the required leg is unaffected |
| `CLAUDE_CONFIG_DIR`, `COPILOT_HOME` | client config homes | LLM-3, VIEW-1 signed-out steps | Set by the scenario to an empty temp dir only for the signed-out step; otherwise inherited from the signed-in user |
| `DSH_SCENARIO_STORE1_*`, `DSH_SCENARIO_STORE6_*` | command templates (existing) | Phase 6 guarded | Built-in templates of `testkit/scenario` over `{dsh}` and `{home}` (see Phase 6 Setup; open question `e2e-guarded-template-defaults`) |
| `DSH_SCENARIO_FS3_EDITS`, `DSH_SCENARIO_FS3_SAVE_INTERVAL` | count, Go duration (new) | DH-E-FS-3 | 1000 edits per process, 5ms between saves |
| `DSH_SCENARIO_STORE2_START`, `_PROMPT`, `_RESUME`, `_REQUEST`, `_KILL` | command templates (existing; `_KEY` removed, M2-I-1) | DH-E-STORE-2 | Required; run fails |
| `DSH_SCENARIO_SDK1_LAYER`, `DSH_SCENARIO_SDK1_LONG_PROMPT` | layer text, prompt (new) | DH-E-SDK-1 | Required |
| `DSH_SCENARIO_SANDBOX7_TASKS`, `DSH_SCENARIO_SANDBOX7_PREVIOUS_SHARE` | task-set dir, previous escalation share (new) | DH-E-SANDBOX-7 | Task set: the committed `testdata/sandbox7/`; previous share: 10/18 floor |
| `DSH_SCENARIO_SANDBOX1_MACHINES` | `;`-separated SSH aliases (new) | DH-E-SANDBOX-1, NATIVESYSTEM-1..4 | Required |
| `DSH_SCENARIO_EXEC1_*`, `DSH_SCENARIO_EXEC10_SSH`, `DSH_SCENARIO_EXEC10_SSH_NOMANAGER`, `DSH_SCENARIO_EXEC10_*` | templates, SSH aliases (existing) | EXEC-1, EXEC-10 | Required |
| `DSH_SCENARIO_HOST4_LAYER`, `_ARGS`, `_READY` | layer, args, readiness probe (existing) | DH-E-HOST-4 | Required |
| `DSH_SCENARIO_STORAGE6_USER`, `_PUBLIC`, `_SEED`, `_LIST`, `_READ`, `_START`, `_STATUS`, `_STOP` | account name, templates (existing) | DH-E-STORAGE-6 | Required |
| `DSH_SCENARIO_SEARCH_BUDGET` | Go duration (existing) | DH-E-RIPGREP-1 | Required; the agreed budget is recorded with the result |
| `DSH_SCENARIO_FS_CHECKOUTS`, `DSH_SCENARIO_FS1_SHARE`, `DSH_SCENARIO_WATCH_SHARES` | checkout dirs, SMB/NFS mount points (new) | FS-1, FS-2, RIPGREP-*, CHOKIDAR-2 | Required on the variant that names them |
| `DSH_SCENARIO_VMS`, `DSH_SCENARIO_NODEFS_*` | VM names, controller templates, ack listener (existing) | DH-E-NODEFS-1 | Required |
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
| `service:os-process` | Test binary re-exec (helper pattern) or built `dsh` from `testkit/dshbin`; reaped by `testkit/procwait` | 3, 5, 6 |
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
| STORE-1, STORE-6 | 6 (guarded) | linux-wsl, windows | TC-19s1 | `store_two_process_scenario_test.go`, `store_deletion_scenario_test.go` |
| FS-3 | 6 (guarded) | linux-wsl, windows | TC-19s1 | `fs_two_process_scenario_test.go` (new) |
| STORE-2 | 7 (guarded) | linux-wsl only (impl.md 16.8) | TC-19s2 | `store_crash_recovery_scenario_test.go` |
| TOOLCHAIN-1 (first), SESSION-1, SESSION-2, AGENT-1..4, LLM-1, LLM-3, VIEW-1, TOOLS-1, LOADER-1, SDK-1, SANDBOX-7 (last) | 8 (acceptance) | Linux E1 | TC-20s | existing files; `sdk_stdio_scenario_test.go`, `sandbox_policy_text_scenario_test.go` (new) |
| TOOLCHAIN-2 (first) | 9 | linux, windows | TC-21s3 | `toolchain_rehearsal_scenario_test.go` |
| HOST-4 | 9 | linux, windows | TC-21s3 | `host_report_scenario_test.go` |
| EXEC-1, EXEC-10 | 9 | Linux E2 (systemd/cgroup VMs) | TC-21s2 | `exec_tree_scenario_test.go`, `exec_scope_ssh_scenario_test.go` |
| SANDBOX-1, NATIVESYSTEM-1..4 | 9 | Linux E2 (bwrap, Landlock-only, old kernel, musl) | TC-21s1 | `sandbox_status_scenario_test.go`, `sandbox_native_scenario_test.go` (new) |
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

- **Scenario:** Seven stage records and one closure record, all open
  ```gherkin
  @proof:golden @venue:gate
  Given the committed docs/stories/harness-DSH-M2/evidence/
  When the record-shape check runs
  Then DH-S-12.md .. DH-S-18.md each hold the impl.md sec. 7.3 fields and read "Closure status: open"
  And closure.md has 35 deliverable rows and rows for H-1, H-2, H-3, H-4, H-10 and H-21
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

impl.md DH-S-14, implementation-plan Phase 4. Components C42 (`agent/preset` with
`standard.yml`) and C43 (`agent/bundle` with the composed-bundle golden). Proves TC-08a,
TC-08g, TC-09b.

### Setup
- **Type**: inline
- **Local**: `go -C src/harness test -race ./internal/agent/preset/... ./internal/agent/bundle/...`
  on both lanes. Regenerating a golden on purpose takes `-update`, and the diff is reviewed in the commit.
- **CI runner**: GitHub-hosted `ubuntu-latest` and `windows-latest`, `harness` job.
- **Secrets**: none.
- **Pre-test bootstrap**: none.
- **Gate provisioning**: no connection env-var. Presets load from embedded files and
  from layer text built in the test. The lease and revision rows use embedded modernc
  SQLite under `t.TempDir()`. No network, no scenario skips.

### Scenarios

#### Feature: The shipped standard preset

- **Scenario:** standard lists exactly the M2 tools
  ```gherkin
  @TC-08a @proof:in-process @venue:gate
  Given the embedded agent/preset/standard.yml
  When it loads
  Then its tool list is "read write edit apply_patch glob grep bash pwsh" in that order
  And its egress allow list is empty
  ```
- **Scenario:** A preset naming an unregistered tool fails load
  ```gherkin
  @TC-08a @proof:in-process @venue:gate @edge
  Given a layer preset whose tool list names "web_fetch"
  When configuration loads
  Then it fails naming "web_fetch" and the layer file:line
  ```

#### Feature: The composed bundle is deterministic

- **Scenario:** Composition equals the golden
  ```gherkin
  @TC-08g @proof:golden @venue:gate
  Given standard.yml, the default permission preset and the test layer under agent/bundle/testdata/
  When bundle.Compose runs on the Linux lane and on the Windows lane
  Then each output equals testdata/composed.golden byte for byte
  ```
- **Scenario:** Every tool in the bundle has a declared effect
  ```gherkin
  @TC-08a @proof:in-process @venue:gate @edge
  Given a composed bundle with one tool lacking an effect declaration
  When the coverage check runs
  Then it fails naming the tool
  ```

#### Feature: A session keeps the preset revision it started with

- **Scenario:** Editing a preset mid-session does not change the running session
  ```gherkin
  @TC-09b @proof:service:sqlite @venue:gate
  Given a session started on preset revision r1 in an embedded SQLite home
  When the preset file is edited to revision r2 and the configuration reloads
  Then the session's next turn still records preset revision r1
  And a new session records r2
  ```
- **Scenario:** Resume keeps the leased revision
  ```gherkin
  @TC-09b @proof:service:sqlite @venue:gate @edge
  Given that session closed and the store reopened by a new process
  When it resumes
  Then the rebuilt bundle equals the r1 bundle, and r1 is not collected while the lease exists
  ```

---

# Phase 5: Listener, Sign-In, Stdio and SDK Server

impl.md DH-S-15, implementation-plan Phase 5. Components C44 (listener and control
endpoint), C45 (`dsh sdk` stdio server), C46 (route table of declared effects), C47
(approval and question answerer), C48 (sign-in check). Proves TC-10b, TC-11b, TC-12a,
TC-12b, TC-12g. Authors the later-run HOST-1, HOST-2, HOST-7, HOST-8 and HOST-9.

### Setup
- **Type**: inline
- **Local**: `go -C src/harness test -race ./internal/host/... ./internal/sdkserver/... ./cmd/dsh/...`
  plus `go -C src/sdk test ./...`, on both lanes. The built-binary rows build `dsh` once
  through `testkit/dshbin`.
- **CI runner**: GitHub-hosted `ubuntu-latest` and `windows-latest`, `harness` job.
- **Secrets**: none. The sign-in rows use per-test cookies and certificates generated in memory.
- **Pre-test bootstrap**: none.
- **Gate provisioning**: no connection env-var. The listener binds `127.0.0.1:0`. The
  control endpoint is a Unix socket under `t.TempDir()` (AF_UNIX on Windows). The model
  is the keyless replay provider. The host's home is a fresh `t.TempDir()` with embedded
  SQLite. Children are reaped and checked by `testkit/procwait`. No scenario skips.

### Scenarios

#### Feature: The listener answers only the signed-in owner (C44, C48)

- **Scenario:** Requests without the cookie, from another origin, or from a tool child are refused
  ```gherkin
  @TC-10b @proof:service:loopback-http @venue:gate @edge
  Given a host listening on 127.0.0.1:0 in a scratch home
  When requests arrive (a) without the cookie, (b) with Origin set to another host, (c) from a process started by a tool with the parent's environment
  Then each gets 401 or 403 and no session event is appended
  ```
- **Scenario:** A tool child gets no session grant
  ```gherkin
  @TC-10b @proof:service:os-process @venue:gate @edge
  Given a bash tool call that prints its environment
  When the result is read
  Then it contains no listener cookie, no control-socket path and no DSH_* grant variable
  ```

#### Feature: dsh sdk speaks frames on stdout only (C45)

- **Scenario:** stdout is frames only
  ```gherkin
  @TC-11b @proof:service:os-process @venue:gate
  Given built dsh started as "dsh sdk" with the replay provider in a scratch home
  When the test sends initialize, session/new and a prompt, then closes stdin
  Then every stdout byte parses as a JSON-RPC frame, and all diagnostics go to stderr
  ```
- **Scenario:** Exit codes
  ```gherkin
  @TC-11b @proof:service:os-process @venue:gate @edge
  Given built dsh in headless mode
  When (a) a turn completes, (b) the turn exceeds "--timeout", (c) the launch cannot start
  Then the exit codes are 0, 124 and 125
  ```
- **Scenario:** The wire schema does not drift
  ```gherkin
  @TC-12g @proof:golden @venue:gate
  Given src/sdk/jsonrpc/schema.json
  When the schema generator runs
  Then its output equals the committed file byte for byte
  ```

#### Feature: Route table and answerer (C46, C47)

- **Scenario:** Every route declares its effect
  ```gherkin
  @TC-12a @proof:in-process @venue:gate
  Given the route table
  When the coverage check runs
  Then every method has one declared effect and an unlisted method is refused "rpc/method-not-found"
  ```
- **Scenario:** An approval is answered once
  ```gherkin
  @TC-12b @proof:service:os-process @venue:gate @edge
  Given an SDK client and a write needing approval
  When the client answers "reject" twice
  Then the first answer is applied and the file is unchanged
  And the second answer is refused as already answered
  ```

#### Feature: S15 scenario authoring

- **Scenario:** Authored S15 scenarios compile and are indexed
  ```gherkin
  @proof:service:go-toolchain @venue:gate
  Given the five new HOST files
  When "go vet -tags scenario" and the scenario-index check run on both lanes
  Then both exit 0
  And HOST-1 and HOST-2 are indexed once under DH-S-22 and HOST-7, HOST-8, HOST-9 once under DH-S-39
  And none imports src/sdk/sdk (M2-DOD-5)
  ```

---

# Phase 6: Profiles, Launchers, Evidence Runners and Keyless Guarded Runs

impl.md DH-S-16, implementation-plan Phase 6 (Stages 6.1-6.11, and 6.12 for the three
keyless guarded runs). Components C49..C56: build profiles, `testkit/dshbin`,
`testkit/scenario` (C55), the Go SDK client `src/sdk/sdk`, and the lane `--guarded` mode.
Proves TC-13b, TC-13g, TC-14b, TC-15b, TC-16b, TC-16g, TC-17a, TC-18a, TC-19s1. Authors
SDK-1, SDK-2, UTIL-2, CLI-1, CLI-2, BUNDLE-1..4 and API-5.

### Setup
- **Type**: inline
- **Local**: gate rows: `go -C src/harness test -race ./internal/testkit/... ./cmd/dsh/...`
  plus `go -C src/sdk test -race ./...`. Guarded rows:
  `tools/harness-gate/lanes/wsl.sh --guarded DH-S-16` and
  `tools/harness-gate/lanes/win.ps1 -Guarded DH-S-16`. Each runs
  `go -C src/harness test -tags scenario -run 'Store1|Store6|FS3' ./internal/testkit/scenarios/`
  with `DSH_REQUIRE_SCENARIOS=DH-E-STORE-1,DH-E-STORE-6,DH-E-FS-3`.
- **CI runner**: GitHub-hosted `ubuntu-latest` and `windows-latest`. Gate rows run in the
  `harness` job of `ci.yml`. Guarded rows run in the `scenarios` matrix of
  `.github/workflows/harness-scenarios.yml` (`workflow_dispatch` with
  `stage: DH-S-16`), after its `preflight` job has run `harness-gate require --stage DH-S-16`.
- **Secrets**: none. The three guarded runs are keyless by construction: replay provider, scratch home, no client.
- **Pre-test bootstrap**: none. Preflight checks only the Go toolchain and the lane shell.
- **Gate provisioning**: no connection env-var is needed. When `DSH_BIN` is unset,
  `testkit/dshbin` builds `dsh` into `t.TempDir()`. When the `DSH_SCENARIO_STORE1_*` and
  `DSH_SCENARIO_STORE6_*` templates are unset, `testkit/scenario` supplies its
  built-in templates over `{dsh}` and `{home}` (`dsh headless --home {home} ...`). The
  replay provider replaces the model. The FS-3 "editor save" is a Go helper process (a
  re-exec of the test binary) that writes a temp file and renames it over the target at
  `DSH_SCENARIO_FS3_SAVE_INTERVAL`. It is provisioned by the test, not installed on the
  machine. Homes, workspaces and SQLite files live under `t.TempDir()`. Set templates
  override the defaults. A required scenario never skips: a missing tool fails preflight.

### Scenarios

#### Feature: Profiles and launchers (C49-C52)

- **Scenario:** Each profile builds and reports itself
  ```gherkin
  @TC-13b @proof:service:go-toolchain @venue:gate
  Given the profiles listed in cmd/dsh/profiles
  When each is built with testkit/dshbin
  Then "dsh version" prints the profile and commit with no "modified" marker on a clean tree
  ```
- **Scenario:** Profile wiring equals the golden
  ```gherkin
  @TC-13g @proof:golden @venue:gate
  Given the default profile
  When its component list is rendered
  Then it equals cmd/dsh/testdata/profile_default.golden
  ```
- **Scenario:** headless answers a prompt with the replay provider
  ```gherkin
  @TC-14b @proof:service:os-process @venue:gate
  Given built dsh, a scratch home and the replay provider
  When "dsh headless 'list the files'" runs in a scratch workspace
  Then it exits 0 and prints the replayed final assistant message
  ```
- **Scenario:** The Go client drives a session end to end
  ```gherkin
  @TC-15b @proof:service:os-process @venue:gate
  Given src/sdk/sdk connected to "dsh sdk" over stdio with the replay provider
  When it creates a session, runs a prompt, rejects one write and closes
  Then the run result equals the last assistant message and the rejected file is unchanged
  ```

#### Feature: Evidence runner and closure plumbing (C53-C56)

- **Scenario:** The scenario kit refuses to touch a real home
  ```gherkin
  @TC-16b @proof:service:os-process @venue:gate @edge
  Given DSH_HOME set in the parent environment
  When testkit/scenario starts dsh
  Then the child environment has no DSH_HOME and the home it uses is under t.TempDir()
  ```
- **Scenario:** The evidence statement equals the golden
  ```gherkin
  @TC-16g @proof:golden @venue:gate
  Given a fake scenario run with a fixed clock and commit
  When the statement is written to DSH_SCENARIO_REPORT
  Then it equals testkit/scenario/testdata/statement.golden
  ```
- **Scenario:** A skipped required scenario fails the run
  ```gherkin
  @TC-18a @proof:in-process @venue:gate @edge
  Given DSH_REQUIRE_SCENARIOS listing DH-E-STORE-1 and a run where STORE-1 calls t.Skip
  When the run ends
  Then it fails naming DH-E-STORE-1 (INV-13)
  ```
- **Scenario:** Authored later-run scenarios compile and are indexed
  ```gherkin
  @TC-17a @proof:service:go-toolchain @venue:gate
  Given the files for SDK-1, SDK-2, UTIL-2, CLI-1, CLI-2, BUNDLE-1..4 and API-5
  When "go vet -tags scenario" and the index check run on both lanes
  Then both exit 0
  And SDK-1 is indexed under DH-S-17, SDK-2 and UTIL-2 under DH-S-39, CLI-1 and CLI-2 under DH-S-27, BUNDLE-3 under DH-S-22, and BUNDLE-1, -2, -4 and API-5 under DH-S-48
  And all 17 later-run ids appear exactly once
  ```

#### Feature: Guarded run -- two processes, one session (DH-E-STORE-1)

- **Scenario:** Ownership is exclusive and recovery is clean
  ```gherkin
  @TC-19s1 @DH-E-STORE-1 @proof:service:sqlite @venue:guarded @platform:linux-wsl @platform:windows
  Given built dsh processes A and B on one scratch home, each step limited to 60 s
  And A has created a session and runs a long turn
  When B resumes the session
  Then B is refused "session/owned" naming A's pid
  When A is killed without a clean stop and B resumes
  Then B owns the session with epoch + 1 and the cut turn ends "turn/end" interrupted
  When a new copy of A resumes
  Then it is refused naming B
  And seq runs 0..n with no gap, and A's lock file and hosts row are gone
  Cleanup: stop B, remove the scratch home
  ```

#### Feature: Guarded run -- deletion and collection (DH-E-STORE-6)

- **Scenario:** Shared images survive, unreferenced ones are collected, a cut deletion is tombstoned
  ```gherkin
  @TC-19s1 @DH-E-STORE-6 @proof:service:sqlite @venue:guarded @platform:linux-wsl @platform:windows
  Given a session S with two images and a fork F that keeps the first image
  When S is deleted
  Then F and the shared image survive and the unreferenced image is collected
  Given a 10,000-event session L
  When its deletion is killed after the first chunk
  Then L is tombstoned and invisible, no L row remains after retention, and the integrity check reports clean
  Cleanup: remove the scratch home
  ```

#### Feature: Guarded run -- two writers and an editor (DH-E-FS-3)

- **Scenario:** Every edit is applied or refused stale, never lost
  ```gherkin
  @TC-19s1 @DH-E-FS-3 @proof:service:filesystem @venue:guarded @platform:linux-wsl @platform:windows
  Given two built dsh processes, each running DSH_SCENARIO_FS3_EDITS (1,000) edits on one file in a scratch workspace
  And the test's editor helper saving the same file (write temp, rename) every DSH_SCENARIO_FS3_SAVE_INTERVAL [binding: helper provisioned in-test]
  When all three finish
  Then each edit is logged either applied or "fs/stale-version"
  And the final bytes equal the last applied write
  When one process is killed while it holds the file lock
  Then the other acquires it within the lock timeout
  Cleanup: stop both processes, remove the scratch home and workspace
  ```
- **Scenario:** Guarded stage results are recorded, not inferred
  ```gherkin
  @TC-19s1 @proof:in-process @venue:guarded @edge
  Given the three guarded runs on both lanes
  When the lane writes evidence/DH-S-16.md
  Then each id has one row per lane reading passed or failed with the statement path
  And DH-S-16 remains open until the DH-E-STORE-2 row from Phase 7 reads passed
  ```

# Phase 7: Guarded Recovery Under the Linux Sandbox

impl.md DH-S-16 step 16.8, DH-E-STORE-2 only (implementation-plan Stage 6.12). TC-19s2.
This is the one guarded run that needs a real model and real confinement, so no test can
self-provision it. It runs on the WSL lane machine before any Phase 8 or Phase 9 run.

### Setup
- **Type**: lab-bare-metal
- **Local**: on the tester's WSL lane machine, signed in to Claude Code, with `bwrap`
  installed and the probe reporting Full: `tools/harness-gate/lanes/wsl.sh --guarded DH-S-16 --only DH-E-STORE-2`.
  This runs `go test -tags scenario -run Store2` with `DSH_REQUIRE_SCENARIOS=DH-E-STORE-2`.
- **CI runner**: ADO pool `forge-lab-hw`, or GitHub self-hosted runner labels
  `[self-hosted, linux, wsl, dsh-bwrap, dsh-signed-in-claude]`. Triggered by the
  `scenarios` job of `harness-scenarios.yml` with `stage: DH-S-16-store2`. The runner
  belongs to a person: the signed-in client is that person's session (M2-C-11).
- **Secrets**: no model key exists or is stored (M2-NG-2). Machine-access secrets only:
  - Azure Key Vault `kv-forge-dsh-lab`, entry `dsh-lab-runner-token` (runner registration), exposed through ADO variable group `dsh-m2-lab`.
  - GitHub environment `dsh-m2-guarded`, secret `DSH_LAB_RUNNER_TOKEN`.
  - The route inputs `DSH_SCENARIO_CLAUDE_CODE_MODEL` and `DSH_SCENARIO_CLAUDE_CODE_EFFORT` are environment variables, not secrets.
  - The names are a proposal; see open question `e2e-lab-secret-names`.
- **Pre-test bootstrap**: `harness-gate preflight --stage DH-S-16-store2`. It checks that
  `claude` is on PATH and signed in (a probe prompt through the route), that `bwrap`
  exists and the sandbox probe reports Full, and that the five `DSH_SCENARIO_STORE2_*`
  templates are set. Any miss **fails** the run, and the row reads
  `not run (<check> preflight failed)`.
- **Gate provisioning**: not applicable, because this phase is `lab-bare-metal`. No row of
  this phase is in any gate required list, and the gate never waits on it. A missing
  dependency is a preflight failure, never a pass.

### Scenarios

#### Feature: A crash in the middle of a sandboxed tool call (DH-E-STORE-2)

- **Scenario:** The next request after recovery carries the unknown outcome
  ```gherkin
  @TC-19s2 @DH-E-STORE-2 @proof:service:model-route-claude-code @proof:service:linux-bwrap @venue:guarded @route:claude-code @platform:linux-wsl
  Given built dsh in a scratch home and workspace, workspace-write under bwrap Full
  And the signed-in Claude Code route [binding: replaces "a real model API key", M2-I-1]
  And a prompt whose turn makes a long bash tool call
  When DSH_SCENARIO_STORE2_KILL kills the host and its children during that call
  And DSH_SCENARIO_STORE2_RESUME resumes the session and DSH_SCENARIO_STORE2_PROMPT sends a new prompt
  Then the cut turn ends interrupted
  And the tool call has the result "outcome unknown"
  And the first request printed by DSH_SCENARIO_STORE2_REQUEST after the new prompt contains that result
  Cleanup: kill the host, keep the home as a lab artifact under DSH_LAB_EVIDENCE_URL, remove the workspace
  ```
- **Scenario:** Signed-out preflight is a failure, not a pass
  ```gherkin
  @TC-19s2 @venue:guarded @edge
  Given the runner with CLAUDE_CONFIG_DIR pointed at an empty directory
  When the preflight runs
  Then it exits non-zero naming the sign-in check
  And the DH-E-STORE-2 row reads "not run (claude-code sign-in preflight failed)" and DH-S-16 stays open
  ```

---

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
- **Pre-test bootstrap**: `harness-gate preflight --stage DH-S-17`. It checks:
  - `DSH_BIN` exists and its checksum is recorded;
  - `claude` is signed in, and `copilot` is signed in for the recorded legs (otherwise those legs read `failed (route preflight)`);
  - `bwrap` is present for SDK-1 and SANDBOX-7;
  - the evidence for DH-S-16 reads closed (`harness-gate close --stage DH-S-16 --check`).

  Any miss fails the run.
- **Gate provisioning**: not applicable, because this phase is `lab-bare-metal`. These
  rows are acceptance only (`@venue:acceptance`) and never gate a merge. A missing
  service fails preflight, and the evidence row reads `not run`, never passed.

### Scenarios

#### Feature: Record and replay come first (DH-E-TOOLCHAIN-1)

- **Scenario:** A recording replays with no client and no network
  ```gherkin
  @TC-20s @DH-E-TOOLCHAIN-1 @proof:service:model-route-claude-code @venue:acceptance @route:claude-code
  Given the scenario directory DSH_SCENARIO_TOOLCHAIN1_DIR and the signed-in route
  When DSH_SCENARIO_TOOLCHAIN1_RECORD records a run
  And DSH_SCENARIO_TOOLCHAIN1_REPLAY replays it twice with empty config homes, stand-in clients that fail if started, and a proxy that refuses every connection
  Then both replays pass with equal transcripts and no client process starts
  Cleanup: remove the temporary homes
  ```

#### Feature: Sessions on a real model (DH-E-SESSION-1, DH-E-SESSION-2)

- **Scenario:** A session rebuilds from its log
  ```gherkin
  @TC-20s @DH-E-SESSION-1 @proof:service:model-route-claude-code @venue:acceptance @route:claude-code
  Given a scratch home and a session with three real turns
  When the views are dropped and rebuilt
  Then every view equals its pre-drop value, per session_rebuild_scenario_test.go
  ```
- **Scenario:** A killed host resumes the session
  ```gherkin
  @TC-20s @DH-E-SESSION-2 @proof:service:model-route-claude-code @venue:acceptance @route:claude-code @edge
  Given a real turn in progress
  When the host is killed and restarted
  Then the session resumes, the cut turn reads interrupted, and the next turn completes
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
  ```

#### Feature: Model routes (DH-E-LLM-1, DH-E-LLM-3)

- **Scenario:** A real turn on each route (LLM-1)
  ```gherkin
  @TC-20s @DH-E-LLM-1 @venue:acceptance @route:claude-code @route:copilot-cli
  Given both signed-in routes
  When one turn runs on each
  Then the claude-code leg must pass and the copilot-cli leg's result is recorded beside it
  ```
- **Scenario:** Signed out is permanent and has no fallback (LLM-3)
  ```gherkin
  @TC-20s @DH-E-LLM-3 @venue:acceptance @route:claude-code @edge
  Given CLAUDE_CONFIG_DIR pointed at an empty temporary directory
  When a turn starts
  Then the error is "llm/sign-in-required", classified Permanent
  And retries end with "llm/retry-abandoned" and no other route is tried
  When a request sets MaxTokens on a route that cannot honour it
  Then it is refused "llm/unsupported-option"
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
  Then the change is applied within 2 s with the pid unchanged
  And an invalid YAML edit is reported with file:line:col while the old config stays active
  And DSH_SCENARIO_LOADER_STOP exits 0 with no temp file left
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
  When it cancels DSH_SCENARIO_SDK1_LONG_PROMPT
  Then the cancel reply arrives after "turn/end"
  When it kills dsh
  Then the client sees EOF and the exit status
  And a resume reports closedInterruptedTurn with no seq gap
  And stdout held frames only, and no dsh process remains
  ```

#### Feature: Policy text proved last (DH-E-SANDBOX-7)

- **Scenario:** The platform texts make the model escalate
  ```gherkin
  @TC-20s @DH-E-SANDBOX-7 @proof:service:model-route-claude-code @venue:acceptance @route:claude-code
  Given 12 tasks from DSH_SCENARIO_SANDBOX7_TASKS, 3 runs each, for each of 3 platform texts read from the resolver with no backend
  When the runs finish
  Then every run made at least one tool call
  And at least 10 of the 18 expected escalations happened
  And the counts are recorded beside the text version, closing UP-DEBT-found.Q3.2
  ```
- **Scenario:** Text edited after the pass reopens the proof
  ```gherkin
  @TC-20s @DH-E-SANDBOX-7 @venue:acceptance @edge
  Given the passing record and a later edit to a text file
  When "harness-gate policy-text" runs
  Then it fails naming DH-E-SANDBOX-7 until a new run passes on the new version
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
- **Secrets**: no model key. VIEW-5 and FS-2 use the signed-in route on the controller.
  Machine access only:
  - Azure Key Vault `kv-forge-dsh-lab` entries `dsh-lab-ssh-key`, `dsh-lab-known-hosts`,
    `dsh-lab-hyperv-cred`, `dsh-lab-second-user-cred` and `dsh-lab-runner-token`, exposed
    through ADO variable group `dsh-m2-lab`.
  - GitHub environment `dsh-m2-s18` with secrets `DSH_LAB_SSH_KEY`,
    `DSH_LAB_KNOWN_HOSTS`, `DSH_LAB_HYPERV_CRED`, `DSH_LAB_SECOND_USER_CRED` and
    `DSH_LAB_RUNNER_TOKEN`.
  - Machine identities are referenced only through `DSH_SCENARIO_SANDBOX1_MACHINES`,
    `DSH_SCENARIO_EXEC10_SSH`, `DSH_SCENARIO_VMS` and similar env vars.
- **Pre-test bootstrap**: `harness-gate preflight --stage DH-S-18`. Per variant, it checks:
  - SSH reachability;
  - the kernel and Landlock ABI, bwrap, systemd and cgroup v2;
  - the share mounts;
  - the second account;
  - the VM controller templates;
  - the ack recorder listening on `DSH_SCENARIO_NODEFS_LISTEN`.

  A failed check marks only that variant `not run (<check> preflight failed)`, and the stage stays open (M2-C-2).
- **Gate provisioning**: not applicable, because this phase is `lab-bare-metal`. No row
  enters a gate required list. A missing machine is a preflight failure recorded as *not run*, never a pass.

### Scenarios

#### Feature: The release rehearsal comes first (DH-E-TOOLCHAIN-2)

- **Scenario:** Six archives, two executed systems, keyless replay
  ```gherkin
  @TC-21s3 @DH-E-TOOLCHAIN-2 @proof:service:go-toolchain @venue:acceptance @platform:linux-wsl @platform:windows
  Given a clean checkout at the recorded commit
  When the rehearsal builds the release
  Then six archives and one checksum file exist
  And on Linux and on Windows "dsh version" shows no "modified" marker
  And the keyless headless replay passes
  ```

#### Feature: Linux confinement on real kernels (DH-E-SANDBOX-1, DH-E-NATIVESYSTEM-1..4)

- **Scenario:** Status on three machines (SANDBOX-1)
  ```gherkin
  @TC-21s1 @TC-04s @DH-E-SANDBOX-1 @proof:service:linux-bwrap @proof:service:linux-landlock @venue:acceptance @platform:linux-e2:bwrap @platform:linux-e2:landlock @platform:linux-e2:old-kernel
  Given the three machines in DSH_SCENARIO_SANDBOX1_MACHINES
  When a workspace-write command runs on each
  Then (a) reports bwrap Full
  And (b) reports landlock, Full or Partial according to delegated-execution
  And (c) reports landlock Partial, and "require: full" is refused "sandbox/enforcement-insufficient"
  And exit codes 0, 1, 2 and 125 and the stderr texts "bwrap: fatal" and "dsh sandbox-launch: failed" pass through unchanged
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
- **Scenario:** ABI 1-4 reports Partial (NATIVESYSTEM-2)
  ```gherkin
  @TC-21s1 @DH-E-NATIVESYSTEM-2 @venue:acceptance @platform:linux-e2:old-kernel @edge
  Given a kernel with Landlock ABI between 1 and 4
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
  @TC-21s2 @DH-E-EXEC-1 @proof:service:linux-cgroup @venue:acceptance @platform:linux-e2:systemd
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
  ```
- **Scenario:** A long session corrects its own bad patterns (FS-2)
  ```gherkin
  @TC-21s4 @DH-E-FS-2 @venue:acceptance @route:claude-code @platform:linux-wsl @platform:windows
  Given 20 tasks over DSH_SCENARIO_FS_CHECKOUTS on the signed-in route
  When they run
  Then fewer than 5% of grep calls return "search/invalid-pattern", and each one is followed by a corrected call
  And no rg child is started
  And the disk equals the logged edits
  ```
- **Scenario:** The working directory on a real repository (VIEW-5)
  ```gherkin
  @TC-21s4 @DH-E-VIEW-5 @venue:acceptance @route:claude-code @platform:linux-wsl
  Given a session on a git repository with a worktree [binding: the UI directory-picker clause waits for the UI stage]
  When no tool changes the directory and the host moves the session to the worktree
  Then exactly one "workspace-root/change" is emitted
  And the write lands in the worktree, and a write to the original project is denied
  When the worktree is deleted
  Then the session recovers
  When the repository is deleted
  Then the turn is rejected "workdir/unavailable"
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
  When the file is saved by an editor, and separately a git branch switch changes it
  Then each produces exactly one "updated" message
  And a linked sub/AGENTS.md pointing outside the root is refused and named in the diagnostics
  And no turn fails
  ```
- **Scenario:** A second account cannot read (STORAGE-6)
  ```gherkin
  @TC-21s3 @DH-E-STORAGE-6 @venue:acceptance @platform:linux-wsl @platform:windows
  Given DSH_SCENARIO_STORAGE6_USER and data seeded with DSH_SCENARIO_STORAGE6_SEED
  When that user lists and reads blobs, spill and cache
  Then every request is denied
  When the spill directory is made group-writable
  Then the spill component refuses to start, naming the directory, while the rest of the host runs
  ```
- **Scenario:** Crash reports and safe mode (HOST-4)
  ```gherkin
  @TC-21s3 @DH-E-HOST-4 @venue:acceptance @platform:linux-wsl @platform:windows @edge
  Given DSH_SCENARIO_HOST4_LAYER with failure set to load, panic and fault in turn
  When the host starts each time
  Then each run exits non-zero and stderr names a report readable only by the owner
  And the next start prints the unclean-exit line and "--safe"
  And DSH_SCENARIO_HOST4_READY succeeds once the listener answers [binding: M2-DEC-6 replaces "reaches the UI"]
  And after 12 failures 10 reports remain
  ```

#### Feature: Power cuts come last (DH-E-NODEFS-1)

- **Scenario:** No acknowledged write is lost
  ```gherkin
  @TC-21s5 @DH-E-NODEFS-1 @proof:service:vm-power-cut @proof:service:ack-recorder @venue:acceptance
  Given the VMs in DSH_SCENARIO_VMS (a Linux KVM guest and a Windows Hyper-V guest) and the ack recorder on DSH_SCENARIO_NODEFS_LISTEN
  When each guest is cut 100 times with DSH_SCENARIO_NODEFS_POWEROFF during durable replaces
  Then after each DSH_SCENARIO_NODEFS_POWERON the file is complete and never older than the last acknowledged version
  Cleanup: power the guests on, keep the version log under DSH_SCENARIO_REPORT
  ```
- **Scenario:** Unavailable variants keep the stage open
  ```gherkin
  @TC-21s5 @venue:acceptance @edge
  Given the Hyper-V host failing preflight
  When the stage record is written
  Then the Windows guest row reads "not run (hyperv preflight failed)"
  And "harness-gate ready --stage DH-S-18" fails naming it
  ```

---

# Phase 10: M2 Milestone Closure

implementation-plan Phase 9, the M2 row of impl.md sec. 3.3. Closure is proved over the
records only: no scenario reruns, nothing is published (M2-NG-*).

### Setup
- **Type**: inline
- **Local**: `GOWORK=off go -C tools/harness-gate run . milestone --story harness:DSH-M2 --root ../..`,
  plus `go -C tools/harness-gate test ./...` for the predicate fixtures.
- **CI runner**: GitHub-hosted `ubuntu-latest` and `windows-latest`, `harness` job of `ci.yml`.
- **Secrets**: none.
- **Pre-test bootstrap**: none.
- **Gate provisioning**: no connection env-var. The predicate reads committed records,
  or fixture trees copied into `t.TempDir()`. No service is needed, and nothing skips.

### Scenarios

#### Feature: The milestone closes only when every stage closed

- **Scenario:** Closure counts
  ```gherkin
  @proof:golden @venue:gate
  Given docs/stories/harness-DSH-M2/evidence/closure.md
  When the milestone predicate runs
  Then it lists 35 deliverables, at most 11 of them done
  And every done row cites an S17 or S18 record (M2-DEC-5)
  ```
- **Scenario:** One open stage blocks the milestone
  ```gherkin
  @proof:in-process @venue:gate @edge
  Given fixture records with DH-S-18 open because one variant reads "not run"
  When the milestone predicate runs
  Then it fails naming DH-S-18 and that variant
  ```
- **Scenario:** Built-through advances only on closure
  ```gherkin
  @proof:in-process @venue:gate
  Given every record DH-S-12..DH-S-18 closed
  When the closure step runs
  Then acceptance-index.yaml reads "built-through: DH-S-18"
  And no release, tag or package is published
  ```

