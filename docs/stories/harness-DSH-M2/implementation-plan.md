---
title: "dsh M2 -- first prompt from the command line and the SDK"
storyId: "harness:DSH-M2"
---

> **Livedoc.** Tick a box only when the work it names has merged (steps) or the proof it names has passed and is recorded (scenarios). This plan executes [impl.md](../../harness/impl.md) stages DH-S-12 to DH-S-18 in impl.md's order, with impl.md's step ids, under the contract of [tech-spec.md](tech-spec.md) (scope, constraints M2-C-1..14, definition of done M2-DOD-1..7) and the design of [architecture.md](architecture.md) (components C31..C57, Test & Environment Contract rows TC-01a..TC-21s5, `service:*` names). Scenario scripts are owned by [e2e-scenarios.md](e2e-scenarios.md). M1 decisions in [../harness-DSH-M1/decisions.md](../harness-DSH-M1/decisions.md) stay in force; a step that follows one cites it by name.
>
> **Reading the tags.** Every step names its impl.md step id, deliverable and the TC row that proves it (architecture.md "Rules for downstream documents" rule 1). Every scenario carries `[proof: ...; deps: ...]` using the four proof kinds of the contract: `in-process` (logic, no I/O), `golden` (a committed snapshot is the oracle), `service:<name>` (a gate-local service from architecture.md's "Gate-local services" table, or a provided service with its preflight), `lab` (not used: M2 has no lab row). Persisted or external state is never `in-process`.
>
> **Venues.** Phases 2 to 6 close on the `gate` venue (both lanes: `tools/harness-gate/lanes/win.ps1` Windows native and `lanes/wsl.sh` Linux under WSL, Go `1.27.4`) plus the `guarded` runs of Stage 6.12. Phases 7 and 8 are the `acceptance` venue and close only through evidence records (M2-DEC-5). *Built* and *done* are never merged (M2-C-3).
>
> **Entry default (M2-DEC-1, Q `m2-entry-m1-closure`).** Build phases 2 to 6 may start on the built M1 contracts; no M2 scenario run (Stage 6.12 onward) and no M2 stage closure happens before Stage 1.2 records M1 as closed.
>
> **Platform scope.** Windows native and Linux under WSL (decision `platforms-windows-wsl`, M2-C-12). macOS rows of any scenario are recorded *out of scope by decision* (Q `m2-macos-rows`), never passed or failed. Model routes are the signed-in Claude Code CLI (required) and Copilot CLI (recorded); no API key exists anywhere (decision `models-signed-in-claude-copilot`, M2-EN-1, M2-NG-2).

# Phase 1: M2 Entry and Evidence Scaffolding

## Dependencies
- _none -- start phase_

## Stage 1.1: M2 Evidence Records and Index Skeleton

### Implementation Steps
- [ ] Create `docs/stories/harness-DSH-M2/evidence/DH-S-12.md` .. `DH-S-18.md` in the impl.md sec. 7.3 form used by the M1 records (environment, commit, scenario, result, artifacts, closure status), each starting `Closure status: open`, with an empty "Owner defaults (M2-C-4)" table listing the impl.md sec. 9.1 defaults that stage must have confirmed before closing (S12: full-access label I2 Q3, UTF-8-only text tools; S13: `partial` when delegated execution cannot be stopped I2 Q4; S15: no SDK session grant I2 Q5, client time zone U-K2-5; S16: second `dsh web` reuses the host I3 Q1, Windows double-click I3 Q8, `dsh headless` progress U-K2-4).
- [ ] Create `docs/stories/harness-DSH-M2/evidence/closure.md` with one row per deliverable of tech-spec sec. 3.2 (35 rows: 30 P0, 5 P1), columns *state* (`built`/`done`), *finishing stage or held-open item*, *proving scenarios*, prefilled from tech-spec sec. 3.2's "Stays built until" table (the 11 done-candidates VIEW-10, SANDBOX-1, HUMAN-4, FS-6, SANDBOX-4, EXEC-3, BUNDLE-1, SDK-6, CLI-9, SDK-8, VIEW-4 and the 24 that stay built), plus rows for held-open items H-1, H-2, H-3, H-4, H-10 and H-21 (M2-I-5).
- [ ] Extend `test/harness/acceptance-index.yaml` (C57) with the executing-stage keys this story needs (`DH-S-16`, `DH-S-17`, `DH-S-18`, `DH-S-22`, `DH-S-27`, `DH-S-39`, `DH-S-48`) without moving `built-through` (stays `DH-S-8` until M1 closure advances it); add a YAML comment per key naming its venue (guarded / acceptance / later).
- [ ] Add to `tools/harness-gate` acceptance check the rule "a stage close is refused while any listed scenario of that stage is missing, skipped or failed, or its evidence record says `open`" (C57; M2-C-2) as a `harness-gate close --stage <id>` subcommand reading the index and `docs/stories/<story>/evidence/<stage>.md`; it exits non-zero naming each blocker.

### Dependencies
- _none -- start stage_

### Test Scenarios
- [ ] Scenario: Close check refuses an open stage -- Given an in-memory index listing `DH-E-STORE-1` under `DH-S-16` and an in-memory evidence record with that row `result: not run`, When the close predicate evaluates `DH-S-16`, Then it returns one blocker naming `DH-E-STORE-1` and `not run`; with the row `passed` and `Closure status: closed` it returns none (C57) [proof: in-process; deps: none -- index and record are string fixtures]
- [ ] Scenario: Skipped counts as not passed -- Given an evidence fixture whose scenario row reads `skipped`, When the close predicate runs, Then the stage is refused and the message contains `skipped is not passed` (M2-C-2, M2-DEC-3) [proof: in-process; deps: none]
- [ ] Scenario: Close subcommand reads committed files -- Given the committed `test/harness/acceptance-index.yaml` and the committed `evidence/DH-S-12.md` .. `DH-S-18.md` skeletons, When `GOWORK=off go -C tools/harness-gate run . close --stage DH-S-17 --root ../..` runs, Then it exits non-zero listing all 14 S17 scenarios as blockers, matching `tools/harness-gate/testdata/close_dh_s_17_open.golden` [proof: golden; deps: committed index and evidence skeletons are the inputs, committed golden is the oracle]

## Stage 1.2: M1 Closure Entry Check

### Implementation Steps
- [ ] Add `harness-gate entry --story harness:DSH-M2` that checks M2-DEC-1 as amended by M1 gap-closure step 12.1.9: every `docs/stories/harness-DSH-M1/evidence/DH-S-1.md` .. `DH-S-11.md` reads `Closure status: closed`, the M1 closure record exists and lists every M1 deliverable as done or built-not-done with a reason, and `built-through` in `test/harness/acceptance-index.yaml` is `DH-S-11`; it exits non-zero naming each missing condition.
- [ ] Wire the entry check as the first job step of every `guarded` and `acceptance` run (Stage 6.12 lane script, Phase 7 and Phase 8 workflow jobs) so no M2 scenario runs before M1 is closed (risk M2-R-04); the gate venue (build steps) does not call it.
- [ ] Record in `evidence/DH-S-12.md` the entry state at the first M2 commit (M1 open or closed, commit SHA) and the answer to Q `m2-entry-m1-closure` in force.

### Dependencies
- phase-m2-entry-and-evidence-scaffolding/stage-m2-evidence-records-and-index-skeleton

### Test Scenarios
- [ ] Scenario: Entry refused while an M1 record is open -- Given eleven in-memory M1 evidence records of which `DH-S-11` reads `Closure status: open`, When the entry predicate runs, Then it fails naming `DH-S-11`; with all eleven closed, a closure record and `built-through: DH-S-11` it passes [proof: in-process; deps: none -- records are string fixtures]
- [ ] Scenario: Entry check against the committed M1 records -- Given the repository's committed `docs/stories/harness-DSH-M1/evidence/` and index, When `harness-gate entry --story harness:DSH-M2` runs today, Then it exits non-zero and its output matches `tools/harness-gate/testdata/entry_m1_open.golden` (all eleven records open, `built-through: DH-S-8`) [proof: golden; deps: committed M1 evidence records and index are the input, committed golden is the oracle]

# Phase 2: DH-S-12 Working Directory Filesystem Seam and Policy Resolver

## Dependencies
- _none -- start phase_

## Stage 2.1: Cycle C-3 Contracts and Fakes

### Implementation Steps
- [ ] impl.md 12.1-12.3 contract commit (cycle C-3, impl.md sec. 10.2; architecture.md R-06): add package `src/harness/internal/world/fs` with the seam contract (`FS` interface, `Op`, `Version`, service keys) and the `util/herr` codes `fs/sandbox-denied`, `fs/stale-version`; add `world/fs/fstest` with an in-memory fake seam and the conformance suite `fstest.Run(t, newFS)` every provider must pass.
- [ ] Add package `world/sandbox` (contract only) with `Policy` (unexported fields, no valid zero value), `Mode` (`read-only`, `workspace-write`, `danger-full-access`), `Resolver` interface (`Resolve`, `Escalate`, `ForHelper`, `GrantRoot`, `SetMode`) and the `sandbox.ModeChanging` event type; add `world/sandbox/sandboxtest` fake resolver.
- [ ] Add package `session/workdir` (contract only) with `workdir.Access` (read-only view for readers), the `WorkingDirectory` fold record (`path`, `workspace`, `root` per architecture.md sec. 4.1) and the `workdir/unavailable` code; add a fake `workdir.Access`.
- [ ] Add an `archtest` rule in `src/harness/internal/archtest`: only `world/fs/ops` may import `world/sandbox/policy`; `session/workdir` and `world/sandbox/policy` may import `world/fs` (contract) but never `world/fs/local` or `world/fs/ops` (no inverted import across C-3).

### Dependencies
- phase-m2-entry-and-evidence-scaffolding/stage-m2-evidence-records-and-index-skeleton

### Test Scenarios
- [ ] Scenario: Policy has no valid zero value -- Given `var p sandbox.Policy`, When it is passed to the fake seam's write check, Then the call returns an `herr` error with code `sandbox/unavailable` rather than allowing or denying silently (TC-01a) [proof: in-process; deps: none]
- [ ] Scenario: Fake seam passes its own conformance suite -- Given `fstest.NewMemFS()`, When `fstest.Run` executes, Then every conformance subtest passes, including fence refusal with `fs/sandbox-denied` and `fs/stale-version` on a stale write (TC-01a) [proof: in-process; deps: none -- in-memory fake]
- [ ] Scenario: Inverted import rejected -- Given a seeded `archtest` fixture where `session/workdir` imports `world/fs/local`, When the archtest rule runs, Then it fails naming the import; the real tree passes [proof: service:go-toolchain; deps: go-toolchain package loader over committed testdata]

## Stage 2.2: Working Directory Fold

### Implementation Steps
- [ ] impl.md 12.1 / DH-D-VIEW-10 (C31): implement `session/workdir` -- the `workingDirectory` fold registered with `session/views`, rebuilt on resume; per-session serialized `Change(path)` that validates through the `world/fs` contract and appends the fold event; only the host moves it (no tool-facing setter).
- [ ] Recovery: on resume or before each tool call, if the directory is gone the fold reports `workdir/unavailable` and the agent loop receives an inbox notice; when it moves, the prompt context line (`Working directory: <path>`) and an inbox notice are emitted.
- [ ] Unit tests on the fake seam and fake clock (TC-01a); seam-backed tests with a deleted and a moved `t.TempDir()` directory (TC-01b).

### Dependencies
- phase-dh-s-12-working-directory-filesystem-seam-and-policy-resolver/stage-cycle-c-3-contracts-and-fakes

### Test Scenarios
- [ ] Scenario: Fold rebuilt from the log -- Given an in-memory event log with two `workingDirectory` changes, When the views are rebuilt, Then `workdir.Access.Path()` equals the second path and the fold's `workspace` and `root` match it (TC-01a) [proof: in-process; deps: none -- memstore and fake seam]
- [ ] Scenario: Concurrent changes serialize -- Given two goroutines calling `Change` on one session inside a `synctest` bubble, When both complete, Then the log holds two ordered events and the final fold equals the later one, with no interleaved partial state (TC-01a) [proof: in-process; deps: none]
- [ ] Scenario: Deleted directory reported -- Given a session whose working directory is a `t.TempDir()` subdirectory that the test then removes, When the next tool call asks `workdir.Access`, Then it returns `workdir/unavailable` and exactly one inbox notice is queued (TC-01b) [proof: service:filesystem; deps: filesystem via `t.TempDir()`]

## Stage 2.3: Fenced Filesystem Seam

### Implementation Steps
- [ ] impl.md 12.2 / DH-D-FS-4 (C32) part 1: implement `world/fs/local` on `os.Root` and `util/fsx` (durable replace); roots and mode come only from the `Policy` passed in, never from its own configuration; a path outside the write roots, including one reached through a symbolic link, returns `fs/sandbox-denied`.
- [ ] Part 2: versioned writes -- every read returns a `Version`; a write with a stale version returns `fs/stale-version` (the DH-E-FS-3 subject).
- [ ] Part 3: implement `world/fs/ops`, the sole importer of `world/sandbox/policy`; it resolves the policy per call, calls the local provider and emits file events through cordis.
- [ ] Run the `fstest` conformance suite against `fs/local` on both lanes; add the symlink-swap race test (directory replaced by a link between check and write); on Windows use a junction where symlink creation is refused and record which in the test log (architecture.md "Gate-local services").

### Dependencies
- phase-dh-s-12-working-directory-filesystem-seam-and-policy-resolver/stage-cycle-c-3-contracts-and-fakes

### Test Scenarios
- [ ] Scenario: Conformance on the real provider -- Given `fs/local` rooted at `t.TempDir()`, When `fstest.Run` executes on the Linux and Windows lanes, Then every subtest passes (TC-01b) [proof: service:filesystem; deps: filesystem via `t.TempDir()` on both lanes]
- [ ] Scenario: Fence holds through a swapped link -- Given a write root `R` and an outside directory `O` in `t.TempDir()`, When a helper goroutine swaps `R/sub` for a link to `O` 1,000 times while the seam writes `R/sub/f`, Then no file ever appears under `O` and each refused write returns `fs/sandbox-denied` (TC-01b) [proof: service:filesystem; deps: filesystem via `t.TempDir()`; junction fallback on Windows recorded]
- [ ] Scenario: Stale version refused -- Given file `f` read at version v1 and then rewritten to v2 by another handle, When a write with v1 is attempted, Then it returns `fs/stale-version` and the file still holds v2 bytes (TC-01b) [proof: service:filesystem; deps: filesystem via `t.TempDir()`]

## Stage 2.4: Policy Resolver

### Implementation Steps
- [ ] impl.md 12.3 / DH-D-SANDBOX-1 (C33): implement `world/sandbox/policy` -- `Resolve(mode, workdir, workspace, grants)` with the area-spec precedence, `Escalate`, `ForHelper`, `GrantRoot`, `SetMode` (writes `sandbox/mode` to the log and dispatches `sandbox.ModeChanging`).
- [ ] Render the model-visible policy section per platform text (Linux, macOS, Windows) from the resolver, following the `human/approval/testdata/policy_section.golden` pattern; commit `world/sandbox/policy/testdata/policy_section_{linux,darwin,windows}.golden` and record the text version string that DH-E-SANDBOX-7 will cite (M2-C-6).
- [ ] Apply the unconfirmed default "label of full access" (impl.md sec. 9.1, I2 Q3) as a single constant referenced by the golden, so an owner change is a one-line diff.

### Dependencies
- phase-dh-s-12-working-directory-filesystem-seam-and-policy-resolver/stage-cycle-c-3-contracts-and-fakes

### Test Scenarios
- [ ] Scenario: Resolver precedence -- Given mode `workspace-write`, working directory `W`, workspace root `P` and a grant `G`, When `Resolve` runs, Then write roots are exactly `{P, G}` plus scratch, and `read-only` yields no write roots (TC-01a) [proof: in-process; deps: none -- fake workdir and seam]
- [ ] Scenario: SetMode is logged and announced -- Given an in-memory session, When `SetMode(danger-full-access)` runs, Then one `sandbox/mode` event is appended and one `sandbox.ModeChanging` dispatch is observed before the mode takes effect (TC-01a) [proof: in-process; deps: none]
- [ ] Scenario: Policy section text per platform -- Given the resolver for each of `linux`, `darwin`, `windows`, When the policy section is rendered, Then each output is byte-identical to its committed golden (TC-01g) [proof: golden; deps: committed `policy_section_*.golden`]

## Stage 2.5: Cycle C-3 Integration at One Commit

### Implementation Steps
- [ ] Wire C31, C32 and C33 through boot as one unit: register the three services in the component catalogue, connect `workdir` to `fs/ops` and the resolver to `workdir.Access`; land at one commit so the three deliverables are ticked together (impl.md DH-S-12 "Exit evidence").
- [ ] Add one integration test that drives a session through a directory change, a resolve and a fenced write on the real provider.
- [ ] Record the shared commit SHA for VIEW-10, FS-4 and SANDBOX-1 in `evidence/DH-S-12.md`.

### Dependencies
- phase-dh-s-12-working-directory-filesystem-seam-and-policy-resolver/stage-working-directory-fold
- phase-dh-s-12-working-directory-filesystem-seam-and-policy-resolver/stage-fenced-filesystem-seam
- phase-dh-s-12-working-directory-filesystem-seam-and-policy-resolver/stage-policy-resolver

### Test Scenarios
- [ ] Scenario: Directory move changes the fence -- Given a booted spine with `workspace-write` and working directory `A` in `t.TempDir()`, When the host moves the session to `B` (outside `A`'s workspace) and a write to `A/x` is attempted, Then it returns `fs/sandbox-denied` and `B/x` succeeds (TC-01b) [proof: service:filesystem; deps: filesystem via `t.TempDir()`, sqlite (embedded modernc) for the session log]
- [ ] Scenario: Resume restores the fence -- Given that session persisted in an embedded SQLite store in `t.TempDir()`, When the process reopens the store and resumes, Then the rebuilt working directory is `B` and the same write decisions hold (TC-01b) [proof: service:sqlite; deps: embedded modernc SQLite in `t.TempDir()`, filesystem]

## Stage 2.6: Permissions Choice

### Implementation Steps
- [ ] impl.md 12.4 / DH-D-HUMAN-4 (C34, P1 pulled forward): add `human/permission` -- named presets of mode plus approval policy; folds into `permissions`; pins a fresh session at agent creation; switches through `sandbox.SetMode` and `approval.SetPolicy` together; registers `/permission` in `human/command`.
- [ ] Config load fails with a `78`-mapped configuration error when the default preset does not resolve.

### Dependencies
- phase-dh-s-12-working-directory-filesystem-seam-and-policy-resolver/stage-cycle-c-3-integration-at-one-commit

### Test Scenarios
- [ ] Scenario: Switch moves mode and approval together -- Given a session pinned to preset `ask`, When `/permission auto` runs, Then exactly one `sandbox/mode` and one approval-policy event are appended in that order and both views reflect `auto` (TC-02a) [proof: in-process; deps: none -- memstore]
- [ ] Scenario: Unresolvable default refused -- Given a config layer whose default preset names a missing preset, When config loads, Then load fails with a configuration error naming the preset (TC-02a) [proof: in-process; deps: none]

## Stage 2.7: Six File Tools

### Implementation Steps
- [ ] impl.md 12.5 / DH-D-FS-6 (C35): add `world/fs/tools` with `read`, `write`, `edit`, `apply_patch` (on by default, DH-ADR-35), `glob`, `grep` from Go argument structs, registered in `tools.Service`; every access goes through `fs/ops`.
- [ ] Bounded results with spill (`storage/spill`); `read`, `glob`, `grep` declare overlapping reads, the writers declare writes on the path key; `grep` uses `util/search` (no external `rg` child).
- [ ] Apply the unconfirmed default "text tools are UTF-8 only" (impl.md sec. 9.1) as one documented check in `read`/`edit`.

### Dependencies
- phase-dh-s-12-working-directory-filesystem-seam-and-policy-resolver/stage-cycle-c-3-integration-at-one-commit

### Test Scenarios
- [ ] Scenario: Tools change files exactly as logged -- Given a workspace in `t.TempDir()`, When `write`, `edit` and `apply_patch` run through the tool pipeline, Then the files on disk equal the expected bytes and each tool result in the log names the same path and version (TC-03b) [proof: service:filesystem; deps: filesystem via `t.TempDir()`]
- [ ] Scenario: grep needs no rg -- Given `PATH` scrubbed to an empty directory in the test, When `grep` searches 1,000 files, Then it returns matches and the subprocess fake records zero spawns (TC-03b) [proof: service:filesystem; deps: filesystem via `t.TempDir()`]
- [ ] Scenario: Large result spills -- Given a 5 MiB file, When `read` runs with the default bound, Then the result is truncated at the bound and carries a spill reference whose content equals the full file (TC-03b) [proof: service:filesystem; deps: filesystem via `t.TempDir()` for workspace and spill directory]

## Stage 2.8: DH-S-12 Scenarios and Built Record

### Implementation Steps
- [ ] Author `DH-E-FS-1` (10,000 calls under a link-swap helper) and `DH-E-FS-2` (long-running fence) as `src/harness/internal/testkit/scenarios/fs_fence_scenario_test.go` and `fs_long_scenario_test.go` (`//go:build scenario`), indexed under `DH-S-18` (tech-spec sec. 4.2 rows 34, 35).
- [ ] Author `DH-E-FS-3` (two `dsh` processes and an editor save script, 1,000 edits each, `fs/stale-version`) as `fs_two_process_scenario_test.go`, indexed under `DH-S-16` (guarded).
- [ ] Author `DH-E-VIEW-5` (working directory on a real repository through the signed-in route, M2-I-1) as `view_workdir_scenario_test.go`, indexed under `DH-S-18`.
- [ ] Author `DH-E-SANDBOX-7` (12 tasks x 3 runs x 3 platform texts, pass at >= 10 of 18 escalations, reads texts from the resolver without a backend) as `sandbox_policy_text_scenario_test.go`, indexed under `DH-S-17`; it closes `UP-DEBT-found.Q3.2`.
- [ ] Record the two manual checks of impl.md DH-S-12 (write tool asked for an outside path through a link: denial; directory swapped for a link during a write: fence held) on a scratch directory with commit SHA in `evidence/DH-S-12.md`; record owner answers for the S12 defaults (M2-C-4) or mark them pending (Q `m2-unconfirmed-defaults`).

### Dependencies
- phase-dh-s-12-working-directory-filesystem-seam-and-policy-resolver/stage-permissions-choice
- phase-dh-s-12-working-directory-filesystem-seam-and-policy-resolver/stage-six-file-tools

### Test Scenarios
- [ ] Scenario: New scenarios compile and are indexed -- Given the five new scenario files, When `go -C src/harness vet -tags scenario ./internal/testkit/scenarios/...` and the existing scenario-index check run, Then both exit 0 and each ID appears exactly once in `acceptance-index.yaml` under its executing stage (M2-DOD-5) [proof: service:go-toolchain; deps: go-toolchain on both lanes]
- [ ] Scenario: S12 gate green on both lanes -- Given the stage's merged commits, When `harness-gate` runs on `win.ps1` and `wsl.sh`, Then every gate passes and `evidence/DH-S-12.md` records both lane results with the commit SHA (M2-DOD-1) [proof: service:go-toolchain; deps: go-toolchain, filesystem, os-process on both gate lanes]

# Phase 3: DH-S-13 Sandbox Launch Linux Backends and Shell Tools

## Dependencies
- phase-dh-s-12-working-directory-filesystem-seam-and-policy-resolver

## Stage 3.1: Sandbox Launch Stage

### Implementation Steps
- [ ] impl.md 13.1 / DH-D-SANDBOX-2 (C36): add `world/sandbox/launch` and dispatch the hidden `dsh sandbox-launch` mode from `src/harness/cmd/dsh/main.go` (the mode name is already reserved in `cli/tree.go`); the launcher is the dsh binary itself, no helper is installed.
- [ ] Two-phase protocol over a close-on-exec descriptor pair (architecture.md sec. 4.2, INV-14): for a `confined` plan confine self, try the canary write, send `ready{backend, governed effects, canary}`; for an `unconfined` plan skip both and send `canary: not-tried`; wait; exec the command only after `go`; host rejection or EOF in either direction ends the stage with no command run (`sandbox/launch-failed`).
- [ ] A test hook (build-tag `testhook` only) that forces the stage to report canary `written`, used by TC-04b.

### Dependencies
- _none -- start stage_

### Test Scenarios
- [ ] Scenario: No command before go -- Given the test binary re-executed as `sandbox-launch` with the `none` backend and a marker-writing helper as the command, When the host reads `ready` and then closes the channel without `go`, Then the marker file never exists and the stage exits non-zero (TC-04b) [proof: service:os-process; deps: os-process (helper re-exec) and filesystem (`t.TempDir()` marker) on both lanes]
- [ ] Scenario: Unconfined plan reaches go -- Given a `danger-full-access` plan with backend `none`, When the host answers `go`, Then the marker exists and the reported confinement is `Unconfined` with `canary: not-tried` (TC-04b) [proof: service:os-process; deps: os-process, filesystem on both lanes]
- [ ] Scenario: Ready message decoding -- Given byte strings for valid and truncated `ready` frames, When the decoder parses them, Then valid frames yield the backend and effects and truncated frames return `sandbox/launch-failed` (TC-04a) [proof: in-process; deps: none]

## Stage 3.2: Launch Plan Required for Every Command

### Implementation Steps
- [ ] impl.md 13.2 / DH-D-EXEC-4 (C38): change `world/subprocess` so `Spec.Launch` is required; a spec without a plan is refused before any spawn; the sequence is `Wrap`, start the stage, read `ready`, call the `Confirm` hook (C37 contract), send `go` or close, `Release`; the outcome carries the confirmed `Confinement`.
- [ ] Route the signed-in client routes (`llm/provider/claudecode`, `llm/provider/copilot`) through an explicit unconfined helper plan from `ForHelper` so they keep working (one spawn path, M2-C-9, M2-NG-3).
- [ ] Extend the `onespawn` analyzer in `tools/harness-gate/analyzers` to also reject a `subprocess.Spec` literal without `Launch` outside tests.

### Dependencies
- phase-dh-s-13-sandbox-launch-linux-backends-and-shell-tools/stage-sandbox-launch-stage

### Test Scenarios
- [ ] Scenario: Spec without a plan refused -- Given `subprocess.Spec{Argv: ...}` with no `Launch`, When `Start` is called, Then it returns an `herr` error and the fake spawner records zero starts (TC-04a) [proof: in-process; deps: none -- fake spawner]
- [ ] Scenario: Rejected confirm leaves no command -- Given a real re-exec stage and a `Confirm` hook that rejects, When `Start` runs, Then the marker is absent, the outcome is an error and `Release` ran once (TC-04b) [proof: service:os-process; deps: os-process, filesystem on both lanes]
- [ ] Scenario: Client routes still start -- Given the existing `claudecode` fake-CLI tests, When they run after the change, Then they pass and each recorded spawn carries a helper plan (regression on M1 TC rows) [proof: service:os-process; deps: os-process with the fake client binary already used by M1 tests]

## Stage 3.3: Sandbox Seam Probe Select Confirm

### Implementation Steps
- [ ] impl.md 13.3 / DH-D-SANDBOX-3 (C37): implement `world/sandbox` service -- probe backends with a canary at start-up, select, `Prepare(ctx, Policy, argv) (Launch, error)` fixing the plan `Kind` (`confined` / `unconfined` / refused `sandbox/unavailable` for a confined policy with no passing backend).
- [ ] On `ready`, compute enforcement as the weaker of the start-up probe and this start's `ready` (unknown counts as not governed; a custom runner is never `Full`); refuse with `sandbox/enforcement-insufficient` when `require: full` is unmet; only then authorize `go`; report `Confinement{State, Backend, Missing, Exposed, Scratch}` (INV-3).
- [ ] Windows: no backend in M2 -- a confined policy is refused `sandbox/unavailable`; only `danger-full-access` yields an `unconfined` plan reported `Unconfined` (M2-C-8, R-05).

### Dependencies
- phase-dh-s-13-sandbox-launch-linux-backends-and-shell-tools/stage-launch-plan-required-for-every-command

### Test Scenarios
- [ ] Scenario: Four-step host decision -- Given fake backends and table rows (confined + canary `written`; confined + `not-tried`; unconfined + `not-tried`; unconfined under `require: full`), When `Confirm` evaluates each, Then results are reject, reject, accept `Unconfined`, reject `sandbox/enforcement-insufficient` (TC-04a) [proof: in-process; deps: none -- fake backends and fake kernel evidence]
- [ ] Scenario: Weaker evidence wins -- Given a probe reporting `Full` and a `ready` missing the network effect, When enforcement is computed, Then it is `Partial` with `Missing: [network]` (TC-04a) [proof: in-process; deps: none]
- [ ] Scenario: Windows refuses confined start -- Given the Windows lane and policy `workspace-write`, When `Prepare` runs, Then it returns `sandbox/unavailable` and no process is started (TC-06b row on Windows) [proof: service:os-process; deps: os-process on the Windows lane]

## Stage 3.4: Linux Backends Bubblewrap and Landlock

### Implementation Steps
- [ ] impl.md 13.4 / DH-D-SANDBOX-4 (C37 local): add `world/sandbox/local` with the bubblewrap backend -- `bwrap` found once by absolute path, argv built from the `Policy` (read-only binds, writable roots, scratch, network unshare).
- [ ] Landlock backend in pure Go syscalls (no cgo, NG-4): ABI probe, rule set from the `Policy`, applied by the launch stage to itself; selected when `bwrap` is absent or fails its probe.
- [ ] Apply the unconfirmed S13 default "`partial` when delegated execution cannot be stopped" (I2 Q4) in the enforcement report and note it in `evidence/DH-S-13.md`.

### Dependencies
- phase-dh-s-13-sandbox-launch-linux-backends-and-shell-tools/stage-sandbox-seam-probe-select-confirm

### Test Scenarios
- [ ] Scenario: bwrap argv from policy -- Given a `workspace-write` policy with roots `{P, G}`, When the bwrap backend builds argv, Then it equals `world/sandbox/local/testdata/bwrap_argv.golden` (TC-04a) [proof: golden; deps: committed argv golden]
- [ ] Scenario: Landlock rule set from policy -- Given a fake kernel reporting ABI 3, When the Landlock backend builds rules, Then write access is granted only beneath `{P, G, scratch}` and ABI-gated effects missing from ABI 3 are listed in `Missing` (TC-04a) [proof: in-process; deps: none -- fake kernel evidence]
- [ ] Scenario: Selection order -- Given fake probes (bwrap fails, Landlock passes), When the service selects, Then backend is `landlock`; with both failing a confined policy is refused `sandbox/unavailable` (TC-04a) [proof: in-process; deps: none]

## Stage 3.5: Bounded Output Capture

### Implementation Steps
- [ ] impl.md 13.5 / DH-D-EXEC-3 (C38): add a `proc` collector in `world/subprocess` -- one ring per stream keeps the tail; readers do not consume; overflow goes to bounded spill (`storage/spill`); `Run`'s hard limit stops the whole tree.

### Dependencies
- phase-dh-s-13-sandbox-launch-linux-backends-and-shell-tools/stage-launch-plan-required-for-every-command

### Test Scenarios
- [ ] Scenario: Flood is bounded and recoverable -- Given a re-exec helper writing 64 MiB to stdout, When it runs under the default ring size, Then memory held by the ring stays at its bound, the tail equals the last bytes written, and the spill file holds the full stream (TC-05b) [proof: service:os-process; deps: os-process helper re-exec, filesystem for spill in `t.TempDir()`]
- [ ] Scenario: Hard limit ends the tree -- Given a helper that spawns a grandchild and sleeps, When `Run`'s hard limit fires, Then both pids are gone within the bound (checked by `testkit/procwait`) (TC-05b) [proof: service:os-process; deps: os-process on both lanes]
- [ ] Scenario: Two readers see the same tail -- Given a ring with 10 KiB written, When two readers read concurrently, Then both observe identical bytes and the ring is not drained (TC-05b logic part) [proof: in-process; deps: none]

## Stage 3.6: Bash and PowerShell Executors

### Implementation Steps
- [ ] impl.md 13.6 / DH-D-EXEC-8 (C39): add `world/shellexec`, `/bash`, `/pwsh`, `/env` -- `Resolve` applies defaults and caps; `Start` builds argv, asks C37 for the plan and starts through C38 (never `os/exec`); foreground and background reads on one execution; explicit `Failed` outcome.
- [ ] `/env` builds the scrubbed environment: names held by `human/secret` never pass to the child (M2-C-11).
- [ ] Leave `UP-DEBT-limit.packages.shell.pwsh-local.7` (pwsh 5.1 non-ASCII stdin) open with a pointer to DH-E-EXEC-7 at S39 in `evidence/DH-S-13.md` (M2-I-2).

### Dependencies
- phase-dh-s-13-sandbox-launch-linux-backends-and-shell-tools/stage-linux-backends-bubblewrap-and-landlock
- phase-dh-s-13-sandbox-launch-linux-backends-and-shell-tools/stage-bounded-output-capture

### Test Scenarios
- [ ] Scenario: bash executor on Linux -- Given the Linux lane with `bash` found by preflight and a `danger-full-access` test layer, When `Start` runs `echo hi; exit 3`, Then the outcome has exit code 3, stdout tail `hi` and confinement `Unconfined` (TC-06b) [proof: service:os-process; deps: os-process and bash on the Linux lane; the row fails, never skips, if bash is missing]
- [ ] Scenario: pwsh executor on Windows -- Given the Windows lane with `pwsh`, When `Start` runs `Write-Output hi; exit 3`, Then exit code 3 and tail `hi` (TC-06b) [proof: service:os-process; deps: os-process and pwsh on the Windows lane]
- [ ] Scenario: Secrets never reach the child -- Given a secret named `DSH_TEST_TOKEN` stored in `human/secret` and present in the parent environment, When the env builder runs, Then the child environment lacks that name (TC-06b logic) [proof: in-process; deps: none]

## Stage 3.7: One-Shot Shell Tools

### Implementation Steps
- [ ] impl.md 13.7 / DH-D-EXEC-9 (C40): add `world/shellexec/tools` with `bash` and `pwsh` tools; the model supplies named arguments only (`command`, `timeout`, `description`), never stdin or environment; the structured outcome carries exit code, tails, spill reference and confinement.

### Dependencies
- phase-dh-s-13-sandbox-launch-linux-backends-and-shell-tools/stage-bash-and-powershell-executors

### Test Scenarios
- [ ] Scenario: Unknown argument rejected -- Given a tool call with arguments `{command, stdin}`, When the tool validates input, Then it returns a validation error naming `stdin` and nothing is started (TC-06b logic) [proof: in-process; deps: none]
- [ ] Scenario: Tool result carries confinement -- Given the Linux lane and a `danger-full-access` layer, When the `bash` tool runs `true` through the tool pipeline, Then the logged result contains `exit_code: 0` and `confinement.state: Unconfined` (TC-06b) [proof: service:os-process; deps: os-process and bash on the Linux lane]

## Stage 3.8: Workspace Instruction Files

### Implementation Steps
- [ ] impl.md 13.8 / DH-D-CONTEXT-2 (C41): add `context/instructions` -- discover `AGENTS.md` and kin from the working directory up to the project root, read bounded through C32 (fence applies), digest, fold `instructions.visible`, render a framed budgeted section.
- [ ] Reconcile on `util/watch` events, tool touches, directory change and resume; commit `context/instructions/testdata/section.golden`.

### Dependencies
- phase-dh-s-13-sandbox-launch-linux-backends-and-shell-tools/stage-one-shot-shell-tools

### Test Scenarios
- [ ] Scenario: Outside link refused -- Given a fake seam where `AGENTS.md` is a link outside the root, When discovery runs, Then that file is skipped with `fs/sandbox-denied` recorded and the section omits it (TC-07a) [proof: in-process; deps: none -- fake seam]
- [ ] Scenario: Rendered section -- Given three nested instruction files in a fake seam exceeding the budget, When the section renders, Then it is byte-identical to `section.golden` (frames escaped, budget truncation marked) (TC-07g) [proof: golden; deps: committed `section.golden`]
- [ ] Scenario: Edit is reconciled -- Given a real `t.TempDir()` workspace with `AGENTS.md`, When the file is rewritten, Then within the watch debounce the fold digest changes and the next rendered section contains the new text (TC-07a watch part) [proof: service:filesystem; deps: filesystem via `t.TempDir()` and `util/watch`]

## Stage 3.9: DH-S-13 Scenarios and Built Record

### Implementation Steps
- [ ] Author `DH-E-NATIVESYSTEM-1` .. `-4` and `DH-E-SANDBOX-1` as `sandbox_native_scenario_test.go` and `sandbox_status_scenario_test.go`, indexed under `DH-S-18`; author `DH-E-CONTEXT-2` as `context_instructions_scenario_test.go`, indexed under `DH-S-18`.
- [ ] Author later-run scenarios `DH-E-SANDBOX-2`, `DH-E-EXEC-7`, `DH-E-EXEC-8`, indexed under `DH-S-39` (compile only, not run; tech-spec sec. 4.5).
- [ ] Record the two manual checks of impl.md DH-S-13 on a Linux host (confined write outside roots: denial and reported enforcement; both backends removed: command did not run) and the S13 default confirmation state (M2-C-4) in `evidence/DH-S-13.md`.

### Dependencies
- phase-dh-s-13-sandbox-launch-linux-backends-and-shell-tools/stage-workspace-instruction-files

### Test Scenarios
- [ ] Scenario: Authored S13 scenarios compile and are indexed -- Given the new files, When `go vet -tags scenario` and the scenario-index check run, Then both exit 0 and the nine IDs appear once each under their executing stages (M2-DOD-5) [proof: service:go-toolchain; deps: go-toolchain on both lanes]
- [ ] Scenario: S13 gate green on both lanes -- Given the stage's merged commits, When `harness-gate` runs on both lanes, Then every gate passes and `evidence/DH-S-13.md` lists the fake-backend tests of every security control with results (impl.md DH-S-13 exit evidence) [proof: service:go-toolchain; deps: go-toolchain, os-process, filesystem on both lanes]

# Phase 4: DH-S-14 Agent Presets and Bundles

## Dependencies
- _none -- start phase_

## Stage 4.1: Profile Definitions and Bundle Membership

### Implementation Steps
- [ ] impl.md 14.1 / DH-D-BUNDLE-1 (C42): define in `boot/profile` the five profile definitions of architecture.md C42 (`admin`, `core` kernel, `web`, `headless`, `sdk`) with `profile.Spine` and each profile's `Required` list; fill `bundles/headless.yml` and `bundles/sdk.yml` (today empty) with their bundle membership; leave `minimal.yml` and `acp.yml` untouched (not among the five).
- [ ] Regenerate `boot/profile/testdata/composed.golden` in the same commit and review the diff (TC-08g; architecture.md rule 5).

### Dependencies
- phase-m2-entry-and-evidence-scaffolding/stage-m2-evidence-records-and-index-skeleton

### Test Scenarios
- [ ] Scenario: Composed profiles match the golden -- Given the five definitions and bundles, When the composer renders each profile, Then the output equals `composed.golden` (TC-08g) [proof: golden; deps: committed `composed.golden`]
- [ ] Scenario: Required component missing fails composition -- Given an in-memory `headless` bundle lacking a `Required` entry, When composition runs, Then it fails naming the missing component (TC-08a) [proof: in-process; deps: none]

## Stage 4.2: Host-Plane Layers Admin Core and Base

### Implementation Steps
- [ ] impl.md 14.2 / DH-D-BUNDLE-2 (C42): add `bundles/admin.yml` and `bundles/base.yml`, extend `core.yml`; implement layer operations `insert`, `merge`, `remove` in `boot/profile/stack.go`; record which layer supplied a value for the preview used by `dsh config show`.
- [ ] Regenerate `composed.golden` in the same commit.

### Dependencies
- phase-dh-s-14-agent-presets-and-bundles/stage-profile-definitions-and-bundle-membership

### Test Scenarios
- [ ] Scenario: Layer operations -- Given `base` then `admin` then a user layer that `remove`s one entry and `merge`s a map, When stacked, Then the result has the entry removed, the map merged key-wise, and each value's source layer recorded (TC-08a) [proof: in-process; deps: none]
- [ ] Scenario: Admin profile composition -- Given the committed layers, When `admin` composes, Then it equals its section of `composed.golden` and contains no agent, model adapter or carrier component (INV-10) (TC-08g) [proof: golden; deps: committed `composed.golden`]

## Stage 4.3: Preset Registry

### Implementation Steps
- [ ] impl.md 14.3 / DH-D-BUNDLE-4 (C43): add `boot/agentpreset` -- declarations, revision list with lease counts, `Extends` validation (no cycles, parent must exist), `Select`, session binding and its fold; each preset runs its own loader.

### Dependencies
- phase-dh-s-14-agent-presets-and-bundles/stage-profile-definitions-and-bundle-membership

### Test Scenarios
- [ ] Scenario: Session keeps its revision -- Given a session bound to preset `standard` rev 1 in an embedded SQLite store, When the preset is edited to rev 2, Then the running session's fold still reads rev 1, rev 1's lease count is 1, and a new session binds rev 2 (TC-09b) [proof: service:sqlite; deps: embedded modernc SQLite in `t.TempDir()`]
- [ ] Scenario: Extends cycle refused -- Given in-memory declarations `a extends b`, `b extends a`, When the registry loads, Then it fails naming both (TC-09b logic) [proof: in-process; deps: none]

## Stage 4.4: Shipped Presets and Persona

### Implementation Steps
- [ ] impl.md 14.4 / DH-D-BUNDLE-5 (C43): ship `boot/agentpreset/presets/standard.yml` (tools of C35 and C40, prompt sections, skills) and `agentpreset/persona` -- scoped prefix/suffix, `Complete`, `Suppress`; persona refuses to load outside a preset.

### Dependencies
- phase-dh-s-14-agent-presets-and-bundles/stage-preset-registry

### Test Scenarios
- [ ] Scenario: Persona outside a preset refused -- Given a config that enables persona at top level, When boot loads it, Then load fails with a configuration error naming persona (TC-09b) [proof: in-process; deps: none]
- [ ] Scenario: Standard preset composes an agent -- Given `standard.yml`, When an agent is created in an embedded store, Then its tool list is exactly `read write edit apply_patch glob grep bash pwsh` and the binding is persisted (TC-09b) [proof: service:sqlite; deps: embedded modernc SQLite in `t.TempDir()`]

## Stage 4.5: Bundle Coverage Check and Optional List

### Implementation Steps
- [ ] impl.md 14.5 / DH-D-BUNDLE-6 (C42): add `bundles/optional.yml` and the coverage check -- every catalogue entry is in some profile or in `optional.yml`, else the gate fails; defaults send nothing off the machine until a route is configured (NG-7).
- [ ] Review the optional list and record it in `evidence/DH-S-14.md`; regenerate `composed.golden` if `optional.yml` changes composition.

### Dependencies
- phase-dh-s-14-agent-presets-and-bundles/stage-host-plane-layers-admin-core-and-base
- phase-dh-s-14-agent-presets-and-bundles/stage-shipped-presets-and-persona

### Test Scenarios
- [ ] Scenario: Unbundled component fails coverage -- Given an in-memory catalogue with component `x` in no profile and not in the optional list, When the coverage check runs, Then it fails naming `x`; adding `x` to `optional.yml` passes (TC-08a) [proof: in-process; deps: none]
- [ ] Scenario: Defaults send nothing -- Given each default profile composed with no route configured, When the egress set is computed, Then it is empty (TC-08a) [proof: in-process; deps: none]

## Stage 4.6: DH-S-14 Built Record

### Implementation Steps
- [ ] Record the two manual checks of impl.md DH-S-14 (composed tree of each profile with the layer that supplied three chosen values; preset edited during a test session keeps its revision) and the coverage result over the components that exist so far in `evidence/DH-S-14.md`.

### Dependencies
- phase-dh-s-14-agent-presets-and-bundles/stage-bundle-coverage-check-and-optional-list

### Test Scenarios
- [ ] Scenario: S14 gate green on both lanes -- Given the stage's merged commits, When `harness-gate` runs on both lanes, Then every gate passes including the coverage check (M2-DOD-1) [proof: service:go-toolchain; deps: go-toolchain on both lanes]
- [ ] Scenario: Record complete -- Given `evidence/DH-S-14.md`, When `harness-gate close --stage DH-S-14 --built` runs, Then it reports no missing manual check or commit field [proof: golden; deps: committed evidence record is the input]

# Phase 5: DH-S-15 Listener Sign-In Stdio and SDK Server

## Dependencies
- phase-dh-s-12-working-directory-filesystem-seam-and-policy-resolver
- phase-dh-s-14-agent-presets-and-bundles

## Stage 5.1: Listener and Embedded Assets

### Implementation Steps
- [ ] impl.md 15.1 / DH-D-HOST-1 (C44): add `host/listen` and `host/assets` -- one `http.Server` on one parsed address, optional TLS that reloads when certificate files change, mounts dispatched by longest prefix only after admission, embedded assets.

### Dependencies
- _none -- start stage_

### Test Scenarios
- [ ] Scenario: Longest-prefix mount after admission -- Given mounts `/api` and `/api/sdk` on `127.0.0.1:0` with an admit-all fake, When `GET /api/sdk/x` is sent, Then the `/api/sdk` handler answers; with an admit-none fake the response is 401 and no handler runs (TC-10b) [proof: service:loopback-http; deps: loopback-http on `127.0.0.1:0`]
- [ ] Scenario: TLS reload -- Given a per-test certificate pair, When the files are replaced with a new pair, Then a new connection presents the new certificate without restart (TC-10b) [proof: service:loopback-http; deps: loopback-http, filesystem for certificate files]

## Stage 5.2: Admission by Source then Cookie

### Implementation Steps
- [ ] impl.md 15.2 / DH-D-HOST-2 (C45): add `host/auth` -- admit by source first (loopback or allowed peer), then by a cookie issued from a one-time launch token; hashed session table in storage, `Issue`, sign-out, revocation, sign-in notice; provide `access.Principal`.

### Dependencies
- phase-dh-s-15-listener-sign-in-stdio-and-sdk-server/stage-listener-and-embedded-assets

### Test Scenarios
- [ ] Scenario: Token is one-time -- Given a launch token, When it is exchanged twice, Then the first exchange sets a cookie and the second returns 401 (TC-10b) [proof: service:loopback-http; deps: loopback-http, sqlite for the hashed session table]
- [ ] Scenario: Refusals -- Given the listener, When requests arrive without cookie, with a foreign `Origin`, and from a source not loopback or allowed, Then each is refused with 401/403 and none reaches a mount (TC-10b) [proof: service:loopback-http; deps: loopback-http]

## Stage 5.3: Host Launch and Control Endpoint

### Implementation Steps
- [ ] impl.md 15.3 / DH-D-HOST-3 (C46): add `host/launch` -- print the address or hand off; write the runtime record and lock under `<home>/run/` only in profiles that bind a listener (never `headless` or `sdk`, M2-C-13); serve the control endpoint on a same-user local socket (`host.status`, `host.stop`, `launch.open`; returns no credential).

### Dependencies
- phase-dh-s-15-listener-sign-in-stdio-and-sdk-server/stage-admission-by-source-then-cookie

### Test Scenarios
- [ ] Scenario: Only listening hosts write a record -- Given a temp home, When a listener profile and a `headless` profile boot in-test, Then `<home>/run/` holds exactly one runtime record, the listener's (TC-11b) [proof: service:filesystem; deps: filesystem via `t.TempDir()`, local-socket]
- [ ] Scenario: Control endpoint returns no credential -- Given a running host, When `host.status` is called over the local socket, Then the reply has address and pid and no token or cookie field (TC-11b) [proof: service:local-socket; deps: local-socket in `t.TempDir()` on both lanes]

## Stage 5.4: Declared-Effect Route Table

### Implementation Steps
- [ ] impl.md 15.4 / DH-D-API-1 (C47): add `host/web` -- `Routes.Handle` validates declarations (each route is a declared effect) and swaps the route table atomically; component cleanup removes its routes and cancels in-flight handlers.

### Dependencies
- phase-dh-s-15-listener-sign-in-stdio-and-sdk-server/stage-listener-and-embedded-assets

### Test Scenarios
- [ ] Scenario: Undeclared route refused -- Given a route without an effect declaration, When `Routes.Handle` registers it, Then it returns an error and the table is unchanged (TC-10b logic) [proof: in-process; deps: none]
- [ ] Scenario: Cleanup cancels in-flight -- Given a slow handler serving a request on `127.0.0.1:0`, When its component is disposed, Then the handler's context is cancelled and a new request gets 404 (TC-10b) [proof: service:loopback-http; deps: loopback-http]

## Stage 5.5: Stdio Owner with Bounded Exit

### Implementation Steps
- [ ] impl.md 15.5 / DH-D-SDK-3 (C48): add `wire/stdio` -- claims stdin/stdout once, redirects stray stdout writes to the log, bounded writer; calls `launch.App.Stop` once per ending: `InputClosed` status 0, `ClientStalled` status 125; boot supplies forced status 124 (M2-C-10).

### Dependencies
- _none -- start stage_

### Test Scenarios
- [ ] Scenario: Stray write redirected -- Given an in-process stdio owner over pipes, When a component calls `fmt.Println("x")`, Then stdout carries no `x` and the log does (TC-12a) [proof: in-process; deps: none -- in-memory pipes]
- [ ] Scenario: Bounded endings -- Given the built `dsh --profile sdk` with the keyless fake model route from a test layer, When stdin closes; when the client stops reading; when the forced deadline elapses, Then exit statuses are 0, 125 and 124 respectively (TC-12b) [proof: service:os-process; deps: os-process, go-toolchain via `testkit/dshbin` on both lanes]

## Stage 5.6: SDK Session Methods

### Implementation Steps
- [ ] impl.md 15.6 / DH-D-SDK-4 (C49 `server.go`): add `wire/sdkserver` implementing the `src/sdk/protocol` Version 1 methods create, resume, prompt, cancel, close, history, list, wait, query, model and working-directory set; connection table of owned sessions and children; `initialize` waits for readiness; resume of a session owned by a live writer is refused `session/owned`.
- [ ] Mark held-open H-1 (schedule hook), H-2 (multi-route validation), H-3 (inline images) as `not built` in `evidence/DH-S-15.md`; no method is added and `schema.json` is unchanged (M2-C-7).

### Dependencies
- phase-dh-s-15-listener-sign-in-stdio-and-sdk-server/stage-stdio-owner-with-bounded-exit

### Test Scenarios
- [ ] Scenario: Create, prompt, history -- Given a `jsonrpctest` peer, fake model and memstore, When the client creates a session, prompts and calls history, Then history holds the user and assistant messages in order (TC-12a) [proof: in-process; deps: none -- `src/sdk/jsonrpc/jsonrpctest`, `testkit/model`, memstore]
- [ ] Scenario: Wire schema unchanged -- Given `internal/genschema`, When `schema.json` is regenerated, Then it is byte-identical to the committed file (TC-12g) [proof: golden; deps: committed `src/sdk/protocol/schema.json`]

## Stage 5.7: Run Results and Scoped Notifications

### Implementation Steps
- [ ] impl.md 15.7 / DH-D-SDK-5 (C49 `forward.go`, `runs.go`): forwarder emits scoped, ordered `session.event` and `session.run.completed`; the run result equals the last assistant message; H-4 (goal stop signal) recorded `not built`.

### Dependencies
- phase-dh-s-15-listener-sign-in-stdio-and-sdk-server/stage-sdk-session-methods

### Test Scenarios
- [ ] Scenario: Ordered scoped events -- Given two sessions on one connection, When both run, Then each `session.event` carries its session id and per-session sequence numbers are strictly increasing with no cross-session leakage (TC-12a) [proof: in-process; deps: none]
- [ ] Scenario: Result equals last assistant text -- Given a fake model replying `A` then `B` in one run, When `session.run.completed` arrives, Then its result text is `B` (TC-12a) [proof: in-process; deps: none]

## Stage 5.8: Approval and Question Answerer

### Implementation Steps
- [ ] impl.md 15.8 / DH-D-SDK-6 (C49 `answer.go`): one answerer per connection sends `approval/request` and `question/ask`, validates answers, cancels on withdrawal; applies the S15 default "no SDK session grant" (I2 Q5, NG-8) and the client time zone default (U-K2-5).

### Dependencies
- phase-dh-s-15-listener-sign-in-stdio-and-sdk-server/stage-run-results-and-scoped-notifications

### Test Scenarios
- [ ] Scenario: Invalid answer rejected -- Given a pending `approval/request`, When the client answers with an option not offered, Then the server returns an `sdk/*` error and the approval stays pending (TC-12a) [proof: in-process; deps: none]
- [ ] Scenario: Withdrawal cancels -- Given a pending question, When the turn is cancelled, Then the client receives the withdrawal and a late answer is rejected (TC-12a) [proof: in-process; deps: none]
- [ ] Scenario: No session grant survives -- Given an approval answered "allow for session" on connection 1, When connection 2 resumes the session, Then the next identical call asks again (TC-12a, NG-8) [proof: in-process; deps: none]

## Stage 5.9: DH-S-15 Scenarios and Built Record

### Implementation Steps
- [ ] Author `DH-E-SDK-1` (Go client over stdio against the built binary) as `sdk_stdio_scenario_test.go`, indexed under `DH-S-17`; author later-run `DH-E-SDK-2`, `DH-E-HOST-7`, `DH-E-HOST-8`, `DH-E-HOST-9` (index `DH-S-39`) and `DH-E-HOST-1`, `DH-E-HOST-2` (index `DH-S-22`), compile only.
- [ ] Record the two manual checks of impl.md DH-S-15 (requests without cookie, from another origin and from a model-started process refused; one session driven by hand over stdio with typed JSON lines), the list of complete SDK methods and the H-1..H-4 open list in `evidence/DH-S-15.md`.

### Dependencies
- phase-dh-s-15-listener-sign-in-stdio-and-sdk-server/stage-host-launch-and-control-endpoint
- phase-dh-s-15-listener-sign-in-stdio-and-sdk-server/stage-declared-effect-route-table
- phase-dh-s-15-listener-sign-in-stdio-and-sdk-server/stage-approval-and-question-answerer

### Test Scenarios
- [ ] Scenario: Authored S15 scenarios compile and are indexed -- Given the seven new files, When `go vet -tags scenario` and the scenario-index check run, Then both exit 0 and each ID is indexed once under its executing stage (M2-DOD-5) [proof: service:go-toolchain; deps: go-toolchain on both lanes]
- [ ] Scenario: S15 gate green on both lanes -- Given the stage's merged commits, When `harness-gate` runs on both lanes, Then every gate passes and TC-12g is unchanged (M2-DOD-1) [proof: service:go-toolchain; deps: go-toolchain, loopback-http, local-socket on both lanes]

# Phase 6: DH-S-16 Profiles Launchers and Evidence Runners

## Dependencies
- phase-dh-s-12-working-directory-filesystem-seam-and-policy-resolver
- phase-dh-s-13-sandbox-launch-linux-backends-and-shell-tools
- phase-dh-s-14-agent-presets-and-bundles
- phase-dh-s-15-listener-sign-in-stdio-and-sdk-server

## Stage 6.1: Mode Layers and Five Profiles

### Implementation Steps
- [ ] impl.md 16.1 / DH-D-BUNDLE-3 (C42): add the mode layers and make the five profiles launchable from `boot.Process(profile)`; `headless` and `sdk` bind no listener and write no runtime record (M2-C-13).
- [ ] Regenerate `composed.golden` in the same commit.

### Dependencies
- _none -- start stage_

### Test Scenarios
- [ ] Scenario: Each profile boots -- Given a temp home, When `boot.Process` runs each of the five profiles in-test with the keyless fake route, Then each reaches ready and stops cleanly; only `web` binds a port (TC-08g plus TC-11b) [proof: service:os-process; deps: os-process, filesystem, sqlite (embedded) on both lanes]
- [ ] Scenario: Mode layer composition -- Given the committed layers, When composed, Then output equals `composed.golden` (TC-08g) [proof: golden; deps: committed `composed.golden`]

## Stage 6.2: Admin Tree and Secret Set

### Implementation Steps
- [ ] impl.md 16.2 / DH-D-CLI-9 (C50): in package `cli` (`tree.go`, architecture.md I-3 keeps it there) route offline commands to the `admin` profile -- no carrier, agent, model adapter or plugin process (INV-10, M2-C-14); read-only commands get a non-writing view.
- [ ] `dsh secret set <name>` reads the value from stdin only (never argv, M2-C-11); `dsh sandbox status` prints the C37 report (`Full`/`Partial`/`Unconfined`, backend, missing and exposed effects).
- [ ] Update `cli/testdata/reference.golden` (TC-13g).

### Dependencies
- phase-dh-s-16-profiles-launchers-and-evidence-runners/stage-mode-layers-and-five-profiles

### Test Scenarios
- [ ] Scenario: Admin tree builds no agent -- Given the built `dsh` and a temp home, When `dsh sandbox status` runs beside a live `headless` process on the same home, Then it exits 0 and its component trace lists no agent, model client or carrier (TC-13b) [proof: service:os-process; deps: os-process, go-toolchain (`testkit/dshbin`), filesystem]
- [ ] Scenario: Secret only from stdin -- Given `dsh secret set k` with `v` on stdin, When it runs, Then the secret store holds `k`, and `dsh secret set k v` (value in argv) is rejected with a usage error (TC-13b) [proof: service:os-process; deps: os-process, filesystem (scratch home secret store)]
- [ ] Scenario: Command reference -- Given the command tree, When the reference is rendered, Then it equals `reference.golden` (TC-13g) [proof: golden; deps: committed `cli/testdata/reference.golden`]

## Stage 6.3: Config Show and Validate Close H-21

### Implementation Steps
- [ ] impl.md 16.10 / DH-D-BOOT-6 (C50): offer the boot preview through `dsh config show` and `dsh config validate` in the admin tree; enable the BOOT-6 tests that waited for the admin tree; mark H-21 closed in `evidence/closure.md`.

### Dependencies
- phase-dh-s-16-profiles-launchers-and-evidence-runners/stage-admin-tree-and-secret-set

### Test Scenarios
- [ ] Scenario: Preview constructs no component -- Given a temp home with a user layer, When `dsh config show --profile headless` runs, Then it prints each value with its source layer and the component trace is empty (TC-13b) [proof: service:os-process; deps: os-process, filesystem]
- [ ] Scenario: Validate rejects bad config -- Given a layer with an unresolvable default permission preset, When `dsh config validate` runs, Then it exits 78 naming the preset (TC-13b) [proof: service:os-process; deps: os-process, filesystem]

## Stage 6.4: Headless and SDK Launchers

### Implementation Steps
- [ ] impl.md 16.3 / DH-D-CLI-2 (C50): add `cli/headless` -- `dsh headless "<task>"` creates or resumes, sends a task or command, waits idle, flushes, prints its interval's last assistant text, stops; `--json` emits export lines; approvals answered on the terminal; applies the `dsh headless` progress default (U-K2-4).
- [ ] `dsh sdk` boots the `sdk` profile with C48 as stdio owner; launcher exit statuses 0, 124, 125, 70, 78 (tech-spec sec. 7.3); record exit 130 as the open alignment I-6.

### Dependencies
- phase-dh-s-16-profiles-launchers-and-evidence-runners/stage-mode-layers-and-five-profiles
- phase-dh-s-16-profiles-launchers-and-evidence-runners/stage-admin-tree-and-secret-set

### Test Scenarios
- [ ] Scenario: Headless prints the last assistant text -- Given the built `dsh` with the keyless fake route answering `hello`, When `dsh headless "say hello"` runs on a temp home, Then stdout is exactly `hello\n` and exit status is 0 (TC-13b) [proof: service:os-process; deps: os-process, go-toolchain, filesystem, sqlite (scratch home)]
- [ ] Scenario: JSON export lines -- Given the same run with `--json`, When it completes, Then every stdout line parses as an export record and the last is the run result (TC-13b) [proof: service:os-process; deps: os-process, go-toolchain]

## Stage 6.5: One Bounded Stop Path

### Implementation Steps
- [ ] impl.md 16.4 / DH-D-CLI-4 (C51): add `cli/lifecycle` -- `lifecycle.Watch` state machine; Ctrl-C, SIGTERM/SIGHUP (console close on Windows), stdin end, `dsh stop` and stdio end share one path to `launch.App.Stop`; second Ctrl-C forces; bounded by `ShutdownTimeout`.

### Dependencies
- phase-dh-s-16-profiles-launchers-and-evidence-runners/stage-headless-and-sdk-launchers

### Test Scenarios
- [ ] Scenario: Every trigger reaches one Stop -- Given the state machine with a counting fake `Stop`, When each trigger fires, Then `Stop` is called exactly once per run and a second Ctrl-C moves to forced (TC-14b logic) [proof: in-process; deps: none]
- [ ] Scenario: Signal during a tool call -- Given `dsh headless` running a long fake tool, When SIGTERM (Linux lane) or a console close event (Windows lane, `terminate_windows_test.go` pattern) is sent, Then the process exits within `ShutdownTimeout` and the log closes the turn (TC-14b) [proof: service:os-process; deps: os-process with signals per lane]

## Stage 6.6: Second Launch and Crash Notice

### Implementation Steps
- [ ] impl.md 16.5 / DH-D-CLI-5 (C52): add `cli/hostclient` -- read the runtime record and lock; `launch.open` returns `opened`, `printed` or `launch/no-channel`; a second `dsh web` reuses the running web host (default I3 Q1); `headless` and `sdk` processes coexist under the session lease; an unclean exit is explained at the next start with the crash report path and `--safe`; `dsh status`, `dsh crash`; statuses 70 and 78.

### Dependencies
- phase-dh-s-16-profiles-launchers-and-evidence-runners/stage-one-bounded-stop-path

### Test Scenarios
- [ ] Scenario: Second web reuses the host -- Given a built `dsh web` running on a temp home, When a second `dsh web` starts on the same home, Then it exits 0 after `launch.open` and only one listener exists (TC-11b) [proof: service:os-process; deps: os-process, local-socket, filesystem on both lanes]
- [ ] Scenario: Crash explained at next start -- Given a first host killed with SIGKILL / `TerminateProcess`, When `dsh status` runs, Then it names the crash report path and suggests `--safe` (TC-11b) [proof: service:os-process; deps: os-process, filesystem]

## Stage 6.7: Go Client Package

### Implementation Steps
- [ ] impl.md 16.6 / DH-D-SDK-8 (C53, P1 pulled forward): add `src/sdk/sdk` -- `Start` takes `Options.Bin` or `DSH_BIN`, requires a home, runs `dsh --home <dir> --profile sdk`; shutdown ladder `shutdown`, wait, terminate, kill; `Run` returns on the matching `session.run.completed`; handler contexts cancel on withdrawal; imports only `src/sdk/jsonrpc` and `src/sdk/protocol` (architecture.md sec. 3.4 rule 5).

### Dependencies
- phase-dh-s-16-profiles-launchers-and-evidence-runners/stage-headless-and-sdk-launchers

### Test Scenarios
- [ ] Scenario: Client drives the built binary -- Given `DSH_BIN` set to the binary built once by the test and the keyless fake route, When the client starts, creates a session and `Run`s a prompt, Then it returns the fake's text and `Close` exits the child with status 0 (TC-15b) [proof: service:os-process; deps: os-process, go-toolchain]
- [ ] Scenario: Shutdown ladder escalates -- Given a child that ignores `shutdown`, When the client closes with a short grace, Then it terminates and, failing that, kills, and `testkit/procwait` sees the pid gone (TC-15b) [proof: service:os-process; deps: os-process]
- [ ] Scenario: Missing home refused -- Given `Options` without a home, When `Start` is called, Then it returns an error and no process starts (TC-15b logic) [proof: in-process; deps: none]

## Stage 6.8: Scenario Helper and Keyless Replay

### Implementation Steps
- [ ] impl.md 16.7 / DH-D-UTIL-8 (C55): add `testkit/scenario` -- starts the built binary by `DSH_BIN`, scratch home per scenario (M2-C-1), deadline, cleanup, one evidence statement per scenario to `DSH_SCENARIO_REPORT`; a required scenario that skips fails (M2-DEC-3); analyzer in `tools/harness-gate/analyzers` forbids scenario files importing `src/harness/internal/...` except the test kit launcher.
- [ ] impl.md 16.7 / DH-D-UTIL-7 (C54): add `llm/replay`, `testkit/fixture`, `testkit/dshbin` and `test/replay` -- replay a committed recording without a key against the built binary; compare log, workspace and model requests after normalization with fixed typed tokens; H-10 (sessions with children) recorded open.
- [ ] Retarget the existing scenarios in `testkit/scenarios/` (`binary_test.go`, `sdkhost_test.go`, `realenv_test.go`) to `testkit/scenario` without rewriting their bodies (architecture.md I-2); replace `DSH_SCENARIO_STORE2_KEY` in `store_crash_recovery_scenario_test.go` with the signed-in route input (M2-I-1, TC-19s2).
- [ ] Add the `harness-replay` gate step (decision `replay-gate-step-dh-s-16`) so `DH-T-TOOLCHAIN-1`, `-3`, `-5`, `-6` run in the gate.

### Dependencies
- phase-dh-s-16-profiles-launchers-and-evidence-runners/stage-headless-and-sdk-launchers
- phase-dh-s-16-profiles-launchers-and-evidence-runners/stage-go-client-package

### Test Scenarios
- [ ] Scenario: Missing input fails, not skips -- Given a scenario requiring `DSH_BIN` with it unset and listed in `DSH_REQUIRE_SCENARIOS`, When it runs, Then it fails with `required scenario skipped` (TC-17a) [proof: in-process; deps: none]
- [ ] Scenario: Internal import forbidden -- Given an `analysistest` fixture scenario importing `internal/agent`, When the analyzer runs, Then it reports one diagnostic (TC-17a) [proof: service:go-toolchain; deps: go-toolchain package loader over committed testdata]
- [ ] Scenario: Replay oracle -- Given the committed recording under `test/replay`, When the built binary replays it into a scratch home, Then the normalized persisted log read back from the SQLite store, the workspace tree and the model requests are byte-identical to the committed expected files (TC-16g, TC-16b) [proof: golden; deps: committed `test/replay` oracle; the run's embedded SQLite store in a `t.TempDir()` scratch home is the persisted log, built `dsh` from go-toolchain]

## Stage 6.9: Session Titles

### Implementation Steps
- [ ] impl.md 16.9 / DH-D-VIEW-4 (C56, P1 pulled forward): add `session/title` -- fallback title, normalization, `Rename`, `Unpin`, `Refresh`, revision; generator over the configured route; folds `title` and `titleInput`; events `session/title-request`, `session/title-attempt`, `session/title`; tests use the replay runner of Stage 6.8.

### Dependencies
- phase-dh-s-16-profiles-launchers-and-evidence-runners/stage-scenario-helper-and-keyless-replay

### Test Scenarios
- [ ] Scenario: Fallback then generated -- Given a session with a replay provider recording a title reply, When the first turn completes, Then the fold first shows the fallback and then the generated title, and the log holds request, attempt and title events in order (TC-18a) [proof: in-process; deps: none -- replay provider C54, memstore]
- [ ] Scenario: Pinned title not overwritten -- Given a renamed (pinned) session, When `Refresh` runs, Then the title is unchanged until `Unpin` (TC-18a) [proof: in-process; deps: none]

## Stage 6.10: WSL Lane Bubblewrap Preflight

### Implementation Steps
- [ ] Extend `tools/harness-gate/lanes/wsl.sh` to install or check `bwrap` on an absolute path and to run `dsh sandbox status` as a preflight for `guarded` runs: anything but backend `bwrap`, state `Full` fails the preflight, never skips (architecture.md I-8, `service:linux-sandbox-bwrap`).
- [ ] Extend `tools/harness-gate/lanes/win.ps1` preflight to fail when `pwsh` is missing (architecture.md "Shells"); document both preflights in `tools/harness-gate/README.md`.

### Dependencies
- phase-dh-s-16-profiles-launchers-and-evidence-runners/stage-admin-tree-and-secret-set

### Test Scenarios
- [ ] Scenario: Preflight parses status -- Given recorded `dsh sandbox status` outputs (`bwrap Full`, `landlock Partial`, `Unconfined`), When the preflight parser evaluates them, Then only the first passes (I-8) [proof: in-process; deps: none -- status text fixtures]
- [ ] Scenario: Lane preflight on the WSL machine -- Given the WSL lane machine, When `lanes/wsl.sh --guarded-preflight` runs, Then it prints backend `bwrap`, state `Full` and exits 0 [proof: service:linux-sandbox-bwrap; deps: provided by the WSL lane machine with `bwrap` installed by this step; preflight fails the run if absent]

## Stage 6.11: Later-Stage Scenario Authoring

### Implementation Steps
- [ ] Author `DH-E-CLI-1`, `DH-E-CLI-2` (index `DH-S-27`, step 27.8), `DH-E-BUNDLE-3` (index `DH-S-22`), `DH-E-UTIL-2` (index `DH-S-39`) and `DH-E-BUNDLE-1`, `DH-E-BUNDLE-2`, `DH-E-BUNDLE-4`, `DH-E-API-5` (index `DH-S-48`) on `testkit/scenario`; compile only (tech-spec sec. 4.5).
- [ ] Verify the 17 later-run scenarios of tech-spec sec. 4.5 (Stages 3.9, 5.9 and this stage) are each indexed once with their executing stage.

### Dependencies
- phase-dh-s-16-profiles-launchers-and-evidence-runners/stage-scenario-helper-and-keyless-replay

### Test Scenarios
- [ ] Scenario: Later-run scenarios compile -- Given all 17 files, When `go vet -tags scenario ./internal/testkit/scenarios/...` runs on both lanes, Then it exits 0 (M2-DOD-5) [proof: service:go-toolchain; deps: go-toolchain on both lanes]
- [ ] Scenario: Index count -- Given the committed `acceptance-index.yaml`, When the index check counts the 17 IDs of tech-spec sec. 4.5, Then each appears once under its stated stage and none under DH-S-16..18 [proof: golden; deps: committed index is the oracle input]

## Stage 6.12: Guarded Scenarios on Scratch Homes

### Implementation Steps
- [ ] impl.md 16.8: add a `guarded` mode to the lane scripts that runs `harness-gate entry` (Stage 1.2), then `harness-gate require --stage DH-S-16`, then the four scenarios with `-tags scenario` on scratch homes (M2-C-1, M2-EN-6).
- [ ] Run `DH-E-STORE-1`, `DH-E-STORE-6`, `DH-E-FS-3` on both lanes (TC-19s1) and `DH-E-STORE-2` on the WSL lane only (TC-19s2, architecture.md I-7); macOS rows recorded out of scope by `platforms-windows-wsl`.
- [ ] Record the cleanup evidence in `evidence/DH-S-16.md`: scratch homes and the file compared before and after; the deleted sessions' rows, blobs and spill files shown gone; the log read from the database with `seq` 0 to n and no gap (impl.md DH-S-16 exit evidence).

### Dependencies
- phase-m2-entry-and-evidence-scaffolding/stage-m1-closure-entry-check
- phase-dh-s-12-working-directory-filesystem-seam-and-policy-resolver/stage-dh-s-12-scenarios-and-built-record
- phase-dh-s-16-profiles-launchers-and-evidence-runners/stage-second-launch-and-crash-notice
- phase-dh-s-16-profiles-launchers-and-evidence-runners/stage-scenario-helper-and-keyless-replay
- phase-dh-s-16-profiles-launchers-and-evidence-runners/stage-wsl-lane-bubblewrap-preflight

### Test Scenarios
- [ ] Scenario: DH-E-STORE-1 two headless hosts -- Given two built `dsh headless` processes on one scratch home with the keyless replay route, When the second resumes the first's live session, Then it is refused `session/owned` naming the owner pid; after the owner is killed the resume succeeds with lease epoch + 1 (TC-19s1) [proof: service:sqlite; deps: embedded SQLite store in the scratch home, os-process and filesystem, both gate lanes in guarded mode]
- [ ] Scenario: DH-E-STORE-6 deletion leaves nothing -- Given a session with blobs and spill files, When it is deleted and a second deletion is interrupted by a kill and restarted, Then no row, blob or spill file of either session remains in the scratch home (TC-19s1) [proof: service:sqlite; deps: embedded SQLite store and filesystem in the scratch home, os-process; both lanes]
- [ ] Scenario: DH-E-FS-3 two processes and an editor -- Given two `dsh` processes driving `edit` 1,000 times each through the replay provider and an editor save script on one file, When all finish, Then every conflicting write returned `fs/stale-version`, no edit was lost silently, and the final file equals the last acknowledged version (TC-19s1) [proof: service:git-and-editor; deps: editor save script installed on each lane machine before the run, os-process, filesystem]
- [ ] Scenario: DH-E-STORE-2 kill mid-turn under the sandbox -- Given the WSL lane with `bwrap Full` and the signed-in Claude Code route, When the host is killed while a sandboxed `sleep 60; write` runs and the session is resumed and prompted, Then the turn shows interrupted, the tool call "outcome unknown", and the first model request read from the session log contains that result; then the session is deleted and nothing remains (TC-19s2) [proof: service:model-route-claude-code; deps: tester's Claude Code sign-in on the WSL lane machine (preflight: probe turn), linux-sandbox-bwrap (Stage 6.10 preflight), embedded SQLite in the scratch home]

## Stage 6.13: DH-S-16 Manual Checks and Built Record

### Implementation Steps
- [ ] Record the manual checks of impl.md DH-S-16 in `evidence/DH-S-16.md`: one `dsh headless` prompt against the signed-in Claude Code route (M2-I-1: replaces "the real API with the key stored by `dsh secret set`"; `dsh secret set` exercised separately with a scratch value) with an approval answered on the terminal; Ctrl-C during a tool call; a second `dsh` beside a running host.
- [ ] Record S16 owner default confirmations (I3 Q1, I3 Q8, U-K2-4; M2-C-4), the open list H-1..H-4 and H-10, and `DH-T-TOOLCHAIN-1`, `-3`, `-5`, `-6` passing.
- [ ] Advance `built-through` in `acceptance-index.yaml` to `DH-S-16` once S12..S16 are built and M1 closure has set it to `DH-S-11` (M2-DOD-1); update `evidence/closure.md` states for S12..S16 deliverables.

### Dependencies
- phase-dh-s-16-profiles-launchers-and-evidence-runners/stage-config-show-and-validate-close-h-21
- phase-dh-s-16-profiles-launchers-and-evidence-runners/stage-session-titles
- phase-dh-s-16-profiles-launchers-and-evidence-runners/stage-later-stage-scenario-authoring
- phase-dh-s-16-profiles-launchers-and-evidence-runners/stage-guarded-scenarios-on-scratch-homes

### Test Scenarios
- [ ] Scenario: S16 close check passes -- Given the committed `evidence/DH-S-16.md` with the four guarded rows `passed`, When `harness-gate close --stage DH-S-16` runs, Then it exits 0; with any guarded row `not run` it exits non-zero naming it (M2-DOD-2) [proof: golden; deps: committed evidence record and index are the inputs]
- [ ] Scenario: Built-through advanced -- Given the committed index, When the index check runs, Then `built-through` reads `DH-S-16` and every S12..S16 evidence record carries a commit SHA for each step (M2-DOD-1) [proof: golden; deps: committed index and evidence records]

# Phase 7: DH-S-17 Acceptance on a Real Model

## Dependencies
- phase-dh-s-16-profiles-launchers-and-evidence-runners

## Stage 7.1: Signed-In Route Runner and S17 Workflow

### Implementation Steps
- [ ] Add an `s17` job to `.github/workflows/harness-scenarios.yml` (keep `preflight` plus `scenarios`, M2-DEC-2) that runs only on a labelled self-hosted Linux runner whose user is signed in to `claude` and Copilot CLI (M2-EN-8), on `workflow_dispatch` only (never `pull_request_target`); its first steps are `harness-gate entry` and the signed-in route preflight of `signedin_routes_test.go`; no secret or key input exists in the file (M2-C-11, M2-NG-2).
- [ ] Derive `DSH_REQUIRE_SCENARIOS` for the job from the `DH-S-17` index entry (all 14 IDs, M2-DEC-3); a missing runner label leaves the job unscheduled and S17 *not run*, never passed (M2-R-01).
- [ ] Write `docs/stories/harness-DSH-M2/evidence/runbook-s17-wsl.md` for running the same job from a developer's signed-in WSL machine (M1 DH-S-11 runbook form), naming which machine the record must cite.

### Dependencies
- _none -- start stage_

### Test Scenarios
- [ ] Scenario: Workflow carries no key -- Given the committed `harness-scenarios.yml`, When the workflow lint in `tools/harness-gate` scans it, Then no input, env or secret reference names a model key and the `s17` job's `runs-on` is a self-hosted label (M2-NG-2) [proof: golden; deps: committed workflow file is the input; expected job shape committed as `tools/harness-gate/testdata/s17_job.golden`]
- [ ] Scenario: Required list derivation -- Given the committed index, When `harness-gate require --stage DH-S-17` runs, Then it prints exactly the 14 S17 IDs of tech-spec sec. 4.2 rows 5-18 [proof: golden; deps: committed index; expected list committed as golden]

## Stage 7.2: Record Then Replay Without a Key

### Implementation Steps
- [ ] impl.md 17.1: run `DH-E-TOOLCHAIN-1` first in S17 (architecture.md rule 7): record a session against the signed-in Claude Code route with the built binary, then replay it with no route and no credential; record binary identity, provider and model id, run ids, export and "request received equals request rebuilt" in `evidence/DH-S-17.md`.

### Dependencies
- phase-dh-s-17-acceptance-on-a-real-model/stage-signed-in-route-runner-and-s17-workflow

### Test Scenarios
- [ ] Scenario: DH-E-TOOLCHAIN-1 record and replay -- Given the S17 machine with the signed-in Claude Code route and a scratch home, When the scenario records a session and replays it with the route disabled, Then the replayed persisted log, workspace and requests equal the recording after normalization (TC-20s) [proof: service:model-route-claude-code; deps: tester's signed-in Claude Code CLI on the S17 machine (preflight: probe turn), embedded SQLite scratch store]
- [ ] Scenario: Evidence row complete -- Given `evidence/DH-S-17.md`, When `harness-gate close --stage DH-S-17` reads the TOOLCHAIN-1 row, Then binary checksum, model id, run id and commit are present (M2-DEC-5) [proof: golden; deps: committed evidence record]

## Stage 7.3: Session and Agent Loop Acceptance

### Implementation Steps
- [ ] impl.md 17.2 and 17.3: run `DH-E-SESSION-1`, `DH-E-SESSION-2`, `DH-E-AGENT-1` .. `DH-E-AGENT-4` against the built binary on the signed-in Claude Code route (retargeted in Stage 6.8); single-process scenarios first, host-killing ones last (impl.md DH-S-17 sequence); record each with run id and export.

### Dependencies
- phase-dh-s-17-acceptance-on-a-real-model/stage-record-then-replay-without-a-key

### Test Scenarios
- [ ] Scenario: DH-E-SESSION-1 and -2 -- Given the S17 machine and scratch homes, When the session scenarios run (rebuild from log; kill and resume), Then each passes its specification's Then clauses and the resumed session's persisted log has no `seq` gap (TC-20s) [proof: service:model-route-claude-code; deps: signed-in Claude Code CLI on the S17 machine, embedded SQLite scratch store, os-process]
- [ ] Scenario: DH-E-AGENT-1 .. -4 -- Given the same machine, When the four agent-loop scenarios run (turn, cancel, budget, stop/kill), Then all four pass and each record cites the prompt count (M2-C-5) (TC-20s) [proof: service:model-route-claude-code; deps: signed-in Claude Code CLI, os-process, scratch store]

## Stage 7.4: Model Tools Titles and Configuration Acceptance

### Implementation Steps
- [ ] impl.md 17.4 to 17.7: run `DH-E-LLM-1`, `DH-E-LLM-3` and `DH-E-VIEW-1` with one subtest per route -- `claude-code` must pass, `copilot-cli` recorded (M2-DEC-4) -- then `DH-E-TOOLS-1` and `DH-E-LOADER-1`; record each result including recorded-leg failures.

### Dependencies
- phase-dh-s-17-acceptance-on-a-real-model/stage-record-then-replay-without-a-key

### Test Scenarios
- [ ] Scenario: DH-E-LLM-1, LLM-3, VIEW-1 two-route legs -- Given both signed-in clients on the S17 machine, When the scenarios run, Then the `claude-code` subtests pass and the `copilot-cli` subtests have a kept result whatever it is (TC-20s) [proof: service:model-route-claude-code; deps: signed-in Claude Code CLI (required) and `service:model-route-copilot` (recorded) on the S17 machine]
- [ ] Scenario: DH-E-TOOLS-1 and DH-E-LOADER-1 -- Given the S17 machine, When the tool pipeline scenario and the configuration-edit scenario run, Then both pass and the log shows the edited config taking effect on the next turn (TC-20s) [proof: service:model-route-claude-code; deps: signed-in Claude Code CLI, filesystem, scratch store]

## Stage 7.5: SDK Client Acceptance

### Implementation Steps
- [ ] impl.md 17.8: run `DH-E-SDK-1` (authored in Stage 5.9) -- the Go client `src/sdk/sdk` drives the built binary over stdio on the signed-in route, answering an approval and a question on the same connection; record run ids.

### Dependencies
- phase-dh-s-17-acceptance-on-a-real-model/stage-record-then-replay-without-a-key

### Test Scenarios
- [ ] Scenario: DH-E-SDK-1 -- Given `DSH_BIN` set to the built binary on the S17 machine, When the scenario creates, prompts, answers an approval, cancels and closes, Then each step's Then clause holds and stdout of the child carried frames only (M2-C-10) (TC-20s) [proof: service:model-route-claude-code; deps: signed-in Claude Code CLI, os-process, scratch home]
- [ ] Scenario: Run result equals last assistant message -- Given the same run, When `session.run.completed` arrives, Then its text equals the last assistant message in the persisted history read back from the scratch store (TC-20s) [proof: service:sqlite; deps: embedded SQLite store in the scratch home of the S17 run]

## Stage 7.6: Sandbox Policy Text Escalation Acceptance

### Implementation Steps
- [ ] impl.md 17.9: run `DH-E-SANDBOX-7` last in S17 (architecture.md R-03) as one budgeted job (M2-C-5): 12 tasks x 3 runs x 3 platform texts read from the resolver (TC-01g version recorded) = 108 runs; shard per platform text if the 2 h job limit is hit (M2-R-06, Q `m2-scenario-deadlines`).
- [ ] On pass (at least 10 of 18 escalations per text), mark `UP-DEBT-found.Q3.2` closed in `evidence/DH-S-17.md`; add the review rule that a policy-text change may not merge until SANDBOX-7 passes on it (M2-C-6) to `tools/harness-gate/README.md`.

### Dependencies
- phase-dh-s-17-acceptance-on-a-real-model/stage-session-and-agent-loop-acceptance
- phase-dh-s-17-acceptance-on-a-real-model/stage-model-tools-titles-and-configuration-acceptance
- phase-dh-s-17-acceptance-on-a-real-model/stage-sdk-client-acceptance

### Test Scenarios
- [ ] Scenario: DH-E-SANDBOX-7 escalation threshold -- Given the three policy texts at the recorded version, When 108 runs complete, Then for each text at least 10 of 18 denied-first tasks escalated and the transcripts are kept (TC-20s) [proof: service:model-route-claude-code; deps: signed-in Claude Code CLI on the S17 machine; no sandbox backend needed]
- [ ] Scenario: Version binding -- Given the evidence row, When it is compared with `policy_section_*.golden`, Then the recorded text version equals the committed golden's version (M2-C-6) [proof: golden; deps: committed policy-section goldens and evidence record]

## Stage 7.7: DH-S-17 Closure Record

### Implementation Steps
- [ ] Complete `evidence/DH-S-17.md`: every one of the 14 scenarios with environment (self-hosted runner label or named WSL machine), commit, binary checksum, model id, result and artifacts; recorded legs kept; set `Closure status: closed` only when `harness-gate close --stage DH-S-17` passes (M2-DOD-3).
- [ ] Update `evidence/closure.md` for the M1 and M2 deliverables the S17 scenarios finish.

### Dependencies
- phase-dh-s-17-acceptance-on-a-real-model/stage-sandbox-policy-text-escalation-acceptance

### Test Scenarios
- [ ] Scenario: S17 closes only when complete -- Given the committed record, When `harness-gate close --stage DH-S-17` runs, Then it exits 0 with all 14 required rows passed; a fixture with one required row `not run` exits non-zero (M2-C-2) [proof: golden; deps: committed evidence record and index]
- [ ] Scenario: Copilot leg kept -- Given the record, When the close check reads LLM-1, LLM-3, VIEW-1, Then each has a `copilot-cli` result row (passed or failed, never missing) (M2-DEC-4) [proof: golden; deps: committed evidence record]

# Phase 8: DH-S-18 Acceptance on Real Operating Systems

## Dependencies
- phase-dh-s-16-profiles-launchers-and-evidence-runners

## Stage 8.1: E2 Variant Fleet and Runbooks

### Implementation Steps
- [ ] Write `docs/stories/harness-DSH-M2/evidence/variants.md` listing every S18 variant of impl.md sec. 7.1 E2 (distributions, kernels with and without Landlock, cgroup v2 delegated and not, `systemd --user` present and absent, user namespaces allowed and denied, Windows native) with its provider per Q `m2-e2-fleet`, the matching `service:*` name and its preflight; macOS rows marked out of scope by `platforms-windows-wsl` (M2-EN-2, M2-EN-3).
- [ ] Add an `s18` job family to `harness-scenarios.yml` per variant label (self-hosted or hosted `ubuntu-latest`/`windows-latest` where the variant exists); each job runs `harness-gate entry`, its service preflight and `DSH_REQUIRE_SCENARIOS` from the `DH-S-18` index entry filtered to the variant.
- [ ] Write manual runbooks under `evidence/` for the power-cut host (`service:vm-power-cut`, `service:ack-recorder`), two-account machine (STORAGE-6) and SMB share (FS-1/FS-2 variants) in the M1 DH-S-11 runbook form (tech-spec sec. 3.4 item 7, M2-EN-4, M2-EN-5).

### Dependencies
- _none -- start stage_

### Test Scenarios
- [ ] Scenario: Variant list covers the index -- Given `variants.md` and the `DH-S-18` index entry, When the index check cross-references them, Then every S18 scenario has at least one in-scope variant or an owner decision removing it (M2-DOD-4) [proof: golden; deps: committed variants file and index]
- [ ] Scenario: Preflight refuses a wrong machine -- Given recorded `dsh sandbox status` outputs, When the `service:linux-landlock-only` preflight evaluates a `bwrap Full` output, Then it fails (architecture.md provided services table) [proof: in-process; deps: none -- status text fixtures]

## Stage 8.2: Release Rehearsal on Every Platform

### Implementation Steps
- [ ] impl.md 18.1: run `DH-E-TOOLCHAIN-2` first in S18 on Linux and Windows variants; every later S18 step uses the rehearsal's binaries; record checksums in `evidence/DH-S-18.md`.

### Dependencies
- phase-dh-s-18-acceptance-on-real-operating-systems/stage-e2-variant-fleet-and-runbooks

### Test Scenarios
- [ ] Scenario: DH-E-TOOLCHAIN-2 on Linux -- Given a Linux variant, When the rehearsal archive is unpacked and `dsh --version` runs, Then it exits 0 and prints the embedded version matching the record (TC-21s3) [proof: service:os-process; deps: Linux variant runner per `variants.md`, filesystem]
- [ ] Scenario: DH-E-TOOLCHAIN-2 on Windows -- Given `service:windows-host`, When the same check runs, Then it passes and the checksum matches (TC-21s3) [proof: service:windows-host; deps: hosted `windows-latest` or the Windows lane machine; preflight: binary checksum]

## Stage 8.3: Crash Report on Each System

### Implementation Steps
- [ ] impl.md 18.2: run `DH-E-HOST-4` on Linux and Windows; its "reaches the UI" clause is checked by the listener answering until S22 (M2-DEC-6, Q `m2-host4-ui-clause`).

### Dependencies
- phase-dh-s-18-acceptance-on-real-operating-systems/stage-release-rehearsal-on-every-platform

### Test Scenarios
- [ ] Scenario: DH-E-HOST-4 Linux -- Given a killed host on a Linux variant, When `dsh` starts again, Then a readable crash report exists, its path is printed, and `--safe` start makes the listener answer (TC-21s3) [proof: service:os-process; deps: Linux variant runner, filesystem]
- [ ] Scenario: DH-E-HOST-4 Windows -- Given the same on `service:windows-host`, When restarted, Then the same holds (TC-21s3) [proof: service:windows-host; deps: hosted `windows-latest` or Windows lane machine]

## Stage 8.4: Linux Process Tree Ownership

### Implementation Steps
- [ ] impl.md 18.3: run `DH-E-EXEC-1` (cgroup v2 tree) and `DH-E-EXEC-10` (SSH login, `systemd --user`, memory limit) on team-kept VMs over SSH, including the machine whose user manager is stopped (process-group fallback reported); WSL results counted only as the WSL variant (M2-R-05).

### Dependencies
- phase-dh-s-18-acceptance-on-real-operating-systems/stage-release-rehearsal-on-every-platform

### Test Scenarios
- [ ] Scenario: DH-E-EXEC-1 tree ended -- Given a delegated cgroup v2 VM, When a command forks a deep tree and is stopped, Then no descendant survives and the owner is reported as the delegated scope (TC-21s2) [proof: service:linux-systemd-cgroup; deps: team-kept VM over SSH with scratch account; preflight records `systemctl --user` state]
- [ ] Scenario: DH-E-EXEC-10 fallback reported -- Given the VM with the user manager stopped, When the scenario runs over SSH, Then the process-group fallback is reported and the memory-limit clause holds as specified (TC-21s2) [proof: service:linux-systemd-cgroup; deps: team-kept VM over SSH]

## Stage 8.5: Linux Confinement and Sandbox Status

### Implementation Steps
- [ ] impl.md 18.4: run `DH-E-NATIVESYSTEM-1` .. `-4` and `DH-E-SANDBOX-1` on the three Linux machines (bwrap, Landlock only, neither); copy the reported enforcement (`Full`/`Partial` and each exposure) from each run into `evidence/DH-S-18.md` (impl.md DH-S-18 exit evidence).

### Dependencies
- phase-dh-s-18-acceptance-on-real-operating-systems/stage-release-rehearsal-on-every-platform

### Test Scenarios
- [ ] Scenario: bwrap machine -- Given `service:linux-sandbox-bwrap`, When SANDBOX-1 and NATIVESYSTEM rows run, Then writes inside roots succeed, outside writes are denied, and `dsh sandbox status` reports `bwrap Full` (TC-21s1) [proof: service:linux-sandbox-bwrap; deps: E2 machine per `variants.md`, preflight `bwrap Full`]
- [ ] Scenario: Landlock-only machine -- Given `service:linux-landlock-only`, When the rows run, Then confinement holds and status reports `landlock` with missing effects named (TC-21s1) [proof: service:linux-landlock-only; deps: team-kept VM over SSH, preflight `landlock`]
- [ ] Scenario: Unconfined machine refuses -- Given `service:linux-unconfined`, When a `workspace-write` command is started and `require: full` is set, Then it is refused (`sandbox/unavailable` / `sandbox/enforcement-insufficient`) and no command runs (TC-21s1) [proof: service:linux-unconfined; deps: team-kept VM over SSH, preflight `Unconfined`]

## Stage 8.6: File Fence and Working Directory on Real Systems

### Implementation Steps
- [ ] impl.md 18.5 and 18.6: run `DH-E-FS-1` (10,000 calls under a link-swap helper; Linux ext4, Windows NTFS, SMB share per runbook), `DH-E-FS-2` (long-running; shard if beyond the 3 h variant limit, M2-R-06) and `DH-E-VIEW-5` (signed-in route, `workspace-write` under `bwrap`); on any outside write stop the stage and keep both listings (M2-R-03).

### Dependencies
- phase-dh-s-18-acceptance-on-real-operating-systems/stage-release-rehearsal-on-every-platform

### Test Scenarios
- [ ] Scenario: DH-E-FS-1 fence on real file systems -- Given ext4 and NTFS variants, When 10,000 tool calls run under the link-swap helper, Then the hash listing of the outside directory is unchanged before and after (TC-21s3) [proof: service:filesystem; deps: real ext4 and NTFS volumes on the Linux variant and `service:windows-host`; SMB share per runbook]
- [ ] Scenario: DH-E-FS-2 and DH-E-VIEW-5 -- Given the tester's Linux machine with the signed-in route, `git`, an editor and `bwrap`, When FS-2 and VIEW-5 run, Then their Then clauses hold and every model-chosen write lands inside the session's working directory (TC-21s4) [proof: service:model-route-claude-code; deps: signed-in Claude Code CLI, `service:git-and-editor`, `service:linux-sandbox-bwrap` on the tester's machine]

## Stage 8.7: Search and File Watching on Real Systems

### Implementation Steps
- [ ] impl.md 18.7 and 18.8: run `DH-E-RIPGREP-1`, `DH-E-RIPGREP-2`, `DH-E-CHOKIDAR-1`, `DH-E-CHOKIDAR-2` on Linux and Windows variants.

### Dependencies
- phase-dh-s-18-acceptance-on-real-operating-systems/stage-release-rehearsal-on-every-platform

### Test Scenarios
- [ ] Scenario: Search on real trees -- Given Linux and Windows variants, When RIPGREP-1 and -2 run, Then results match their specification's expected lists with no external `rg` spawned (TC-21s3) [proof: service:filesystem; deps: real volumes on the variant runners, os-process]
- [ ] Scenario: Watching on real trees -- Given the same variants, When CHOKIDAR-1 and -2 run (create, rename, delete, burst), Then every expected event is observed within the specified bound (TC-21s3) [proof: service:filesystem; deps: real volumes on the variant runners]

## Stage 8.8: Instruction Files and Private Storage on Real Systems

### Implementation Steps
- [ ] impl.md 18.9 and 18.10: run `DH-E-CONTEXT-2` (real editor save and `git` branch switch reach the model; outside link refused) on Linux and Windows, and `DH-E-STORAGE-6` (stored data private to one user) on the two-account machines per runbook.

### Dependencies
- phase-dh-s-18-acceptance-on-real-operating-systems/stage-release-rehearsal-on-every-platform

### Test Scenarios
- [ ] Scenario: DH-E-CONTEXT-2 -- Given a real repository with `AGENTS.md`, When it is edited by the editor script and the branch is switched, Then the next model request contains the new section and an outside link is refused (TC-07s, TC-21s4) [proof: service:git-and-editor; deps: git and editor installed before the run on Linux and Windows, os-process]
- [ ] Scenario: DH-E-STORAGE-6 -- Given a second OS account on the same machine, When it tries to read the first user's harness home, Then every read is denied by the OS (TC-21s3) [proof: service:os-process; deps: two-account machine per runbook (M2-EN-5), filesystem]

## Stage 8.9: Durable Replace Under Power Loss

### Implementation Steps
- [ ] impl.md 18.11: run `DH-E-NODEFS-1` last in S18 (architecture.md rule 7, TC-21s5): per guest OS (Linux on KVM/libvirt, Windows on Hyper-V), 100 hard power-offs at random moments with the ack recorder on the hypervisor host; shard across runs if beyond the 3 h variant limit (M2-R-06); keep the recorder log and any failing overlay.

### Dependencies
- phase-dh-s-18-acceptance-on-real-operating-systems/stage-crash-report-on-each-system
- phase-dh-s-18-acceptance-on-real-operating-systems/stage-linux-process-tree-ownership
- phase-dh-s-18-acceptance-on-real-operating-systems/stage-linux-confinement-and-sandbox-status
- phase-dh-s-18-acceptance-on-real-operating-systems/stage-file-fence-and-working-directory-on-real-systems
- phase-dh-s-18-acceptance-on-real-operating-systems/stage-search-and-file-watching-on-real-systems
- phase-dh-s-18-acceptance-on-real-operating-systems/stage-instruction-files-and-private-storage-on-real-systems

### Test Scenarios
- [ ] Scenario: DH-E-NODEFS-1 Linux guest -- Given a libvirt guest from a disposable overlay, When it is powered off 100 times during `fsx.Replace` loops, Then after every restart the file is complete and never older than the recorder's last acknowledged version (TC-21s5) [proof: service:vm-power-cut; deps: team-kept KVM/libvirt host over SSH plus `service:ack-recorder` on the hypervisor; preflight creates, cuts and deletes a throw-away guest]
- [ ] Scenario: DH-E-NODEFS-1 Windows guest -- Given a Hyper-V guest, When the same 100 cuts run with `Stop-VM -TurnOff`, Then the same holds (TC-21s5) [proof: service:vm-power-cut; deps: team-kept Hyper-V host over PowerShell remoting plus `service:ack-recorder`]

## Stage 8.10: DH-S-18 Closure Record

### Implementation Steps
- [ ] Complete `evidence/DH-S-18.md`: one row per scenario per variant with environment, commit, result, artifacts and copied enforcement; every in-scope variant not run recorded *not run* with reason, which keeps S18 open (M2-DOD-4, M2-C-2); out-of-scope variants named with the deciding owner decision.
- [ ] Set `Closure status: closed` only when `harness-gate close --stage DH-S-18` passes; update `evidence/closure.md` for the deliverables S18 finishes.

### Dependencies
- phase-dh-s-18-acceptance-on-real-operating-systems/stage-durable-replace-under-power-loss

### Test Scenarios
- [ ] Scenario: No partial closure -- Given a record fixture where one in-scope variant of `DH-E-EXEC-10` is `not run`, When the close check runs, Then S18 is refused naming that variant (M2-DOD-4) [proof: in-process; deps: none -- record fixture]
- [ ] Scenario: S18 closes when complete -- Given the committed record with all 19 scenarios passed on every in-scope variant, When `harness-gate close --stage DH-S-18` runs, Then it exits 0 [proof: golden; deps: committed evidence record, `variants.md` and index]

# Phase 9: M2 Milestone Closure

## Dependencies
- phase-dh-s-17-acceptance-on-a-real-model
- phase-dh-s-18-acceptance-on-real-operating-systems

## Stage 9.1: Deliverable Closure Record

### Implementation Steps
- [ ] Finalize `evidence/closure.md` (M2-DOD-6): each of the 35 deliverables `done` or `built` with finishing stage or held-open item; at most the 11 done-candidates of tech-spec sec. 3.2 may read `done`; the 24 others stay `built` (S22: BUNDLE-2, BUNDLE-3, BUNDLE-6, HOST-1, HOST-2; S27: CLI-2, CLI-4, CLI-5; S28: FS-4; S32: CONTEXT-2; S39: SANDBOX-2, SANDBOX-3, EXEC-4, EXEC-8, EXEC-9, HOST-3, SDK-3, UTIL-8; S48: BUNDLE-4, BUNDLE-5, API-1; held open: SDK-4 H-1..H-3, SDK-5 H-4, UTIL-7 H-10); H-21 closed.
- [ ] Confirm every M2-C-4 owner default is recorded confirmed or changed in its stage record (Q `m2-unconfirmed-defaults`).

### Dependencies
- _none -- start stage_

### Test Scenarios
- [ ] Scenario: Done needs every proof -- Given an in-memory closure table marking `CLI-2` done while DH-E-CLI-1 is unrun, When the closure predicate runs, Then it refuses naming `CLI-2` and `DH-E-CLI-1` (M2-C-3) [proof: in-process; deps: none]
- [ ] Scenario: Counts match tech-spec -- Given the committed `closure.md`, When the closure check counts rows, Then there are 35 deliverables, at most 11 `done`, and every `built` row names a stage or H-item (M2-I-5) [proof: golden; deps: committed closure record]

## Stage 9.2: Milestone M2 Declaration and Enabling Check

### Implementation Steps
- [ ] Declare M2 reached in `evidence/closure.md` only when DH-S-17 and DH-S-18 are closed and the Stage 6.12 guarded scenarios passed (impl.md sec. 3.3 M2 row); advance `built-through` per index rules.
- [ ] Verify nothing is enabled for users and no build is published (M2-DOD-7, tech-spec sec. 4.6): no release tag, no `release.yml` run, profiles default to sending nothing.

### Dependencies
- phase-m2-milestone-closure/stage-deliverable-closure-record

### Test Scenarios
- [ ] Scenario: Milestone predicate -- Given fixtures for S17 closed and S18 open, When the milestone check runs, Then M2 is refused; with both closed and 16.8 passed it is accepted [proof: in-process; deps: none]
- [ ] Scenario: Nothing published -- Given the committed `.github/workflows/release.yml`, `.goreleaser.yaml` and default profile bundles at the closing commit, When the release check compares them with the story's base commit and computes the default egress set, Then the release files are unchanged, no `dsh` release job is added, and the egress set is empty (M2-DOD-7, NG-7) [proof: golden; deps: committed release files and bundles; base-commit copies captured as `tools/harness-gate/testdata/release_base.golden`]
