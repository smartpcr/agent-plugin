# DH-S-18 evidence

Closure status: open

Evidence record of [impl.md DH-S-18](../../../harness/impl.md#dh-s-18) (Acceptance of the execution world on real operating systems) for story
`harness:DSH-M2`, in the impl.md sec. 7.3 form of the M1 records
([../../harness-DSH-M1/evidence/README.md](../../harness-DSH-M1/evidence/README.md)). Plan:
[implementation-plan.md](../implementation-plan.md) Phase 8. Decisions of
[../../harness-DSH-M1/decisions.md](../../harness-DSH-M1/decisions.md) stay in force.

**Kind.** Acceptance, P0. Pairs with build stages DH-S-1, DH-S-4, DH-S-6, DH-S-12 and DH-S-13.

**How this record closes** (C57; M2-C-2; [the gate's README](../../../../tools/harness-gate/README.md#stage-closure)):

1. While the line above reads `open`, fill the tables below until `harness-gate ready --stage DH-S-18`
   exits 0. `ready` does not read the status line.
2. Only then does the closing PR change the line to `Closure status: closed`.
3. That PR's gate runs `harness-gate close --stage DH-S-18`: every `ready` condition again, and the
   line reading `closed`. A `closed` line on a record that is not ready is refused `closed-but-not-ready`.

No M2 scenario run and no M2 stage closure happens before M1 is closed (M2-DEC-1; `harness-gate entry`,
implementation-plan Stage 1.2). The four tables below are read by the gate: keep their headings and columns.
A result is `passed`, `failed`, `not run (<reason>)`, `skipped`, `blocked` or `out of scope (<decision>)`;
skipped, blocked, not run and an empty cell count as open (impl.md sec. 7.3, M2-DEC-3), and only a
variant out of scope by a named decision is not a blocker (M2-C-2). A `recorded` leg must run; its failure
is kept and does not block (M2-DEC-4). A commit is a SHA of 7 to 40 lower-case hex digits.

## Steps

An acceptance step is ticked only by its scenarios; the commit is the one the scenarios ran at.

| Step | Deliverable | Commit | Notes |
| --- | --- | --- | --- |
| 18.1 | DH-E-TOOLCHAIN-2 | | not run; blocked, see [Stage 8.2 blockers](#stage-82-blockers) |
| 18.2 | DH-E-HOST-4 | | |
| 18.3 | DH-E-EXEC-1, DH-E-EXEC-10 | | not run; blocked, see [Stage 8.4 blockers](#stage-84-blockers) |
| 18.4 | DH-E-NATIVESYSTEM-1 to -4, DH-E-SANDBOX-1 | | |
| 18.5 | DH-E-FS-1, DH-E-FS-2 | | |
| 18.6 | DH-E-VIEW-5 | | |
| 18.7 | DH-E-RIPGREP-1, DH-E-RIPGREP-2 | | |
| 18.8 | DH-E-CHOKIDAR-1, DH-E-CHOKIDAR-2 | | |
| 18.9 | DH-E-CONTEXT-2 | | |
| 18.10 | DH-E-STORAGE-6 | | |
| 18.11 | DH-E-NODEFS-1 | | run last |

## Scenarios

The 19 scenarios of the steps, one row per operating-system variant (M2-DOD-4). A variant in scope but not run keeps the stage open; macOS rows read out of scope by decision `platforms-windows-wsl` (M2-EN-2). Linux variants of tech-spec sec. 4.3 (M2-EN-3) are refined into one row each when Phase 8 records them; reported enforcement (`full` or `partial`) is copied into Artifacts.

| Scenario | Variant | Venue | Leg | Environment | Commit | Result | Artifacts |
| --- | --- | --- | --- | --- | --- | --- | --- |
| DH-E-TOOLCHAIN-2 | Linux (WSL) | acceptance | required | E2 | | not run (harness-gate entry fails: harness:DSH-M1 not closed, M2-DEC-1) | |
| DH-E-TOOLCHAIN-2 | Windows | acceptance | required | E2 | | not run (harness-gate entry fails: harness:DSH-M1 not closed, M2-DEC-1) | |
| DH-E-HOST-4 | Linux (WSL) | acceptance | required | E2 | | not run (stage not started) | |
| DH-E-HOST-4 | Windows | acceptance | required | E2 | | not run (stage not started) | |
| DH-E-HOST-4 | macOS | acceptance | required | | | out of scope (platforms-windows-wsl) | |
| DH-E-EXEC-1 | Linux (E2) | acceptance | required | E2 | | not run (harness-gate entry fails: harness:DSH-M1 not closed, M2-DEC-1) | |
| DH-E-EXEC-10 | Linux (E2, over SSH) | acceptance | required | E2 | | not run (harness-gate entry fails: harness:DSH-M1 not closed, M2-DEC-1) | |
| DH-E-NATIVESYSTEM-1 | Linux (E2) | acceptance | required | E2 | | not run (stage not started) | |
| DH-E-NATIVESYSTEM-2 | Linux (E2) | acceptance | required | E2 | | not run (stage not started) | |
| DH-E-NATIVESYSTEM-3 | Linux (E2) | acceptance | required | E2 | | not run (stage not started) | |
| DH-E-NATIVESYSTEM-4 | Linux (E2) | acceptance | required | E2 | | not run (stage not started) | |
| DH-E-SANDBOX-1 | Linux (E2) | acceptance | required | E2 | | not run (stage not started) | |
| DH-E-FS-1 | Linux ext4 | acceptance | required | E2 | | not run (stage not started) | |
| DH-E-FS-1 | Windows NTFS | acceptance | required | E2 | | not run (stage not started) | |
| DH-E-FS-1 | macOS | acceptance | required | | | out of scope (platforms-windows-wsl) | |
| DH-E-FS-2 | Linux (WSL) | acceptance | required | E2 | | not run (stage not started) | |
| DH-E-FS-2 | Windows | acceptance | required | E2 | | not run (stage not started) | |
| DH-E-FS-2 | macOS | acceptance | required | | | out of scope (platforms-windows-wsl) | |
| DH-E-VIEW-5 | Linux (WSL) | acceptance | required | E2 | | not run (stage not started) | |
| DH-E-RIPGREP-1 | Linux (WSL) | acceptance | required | E2 | | not run (stage not started) | |
| DH-E-RIPGREP-1 | Windows | acceptance | required | E2 | | not run (stage not started) | |
| DH-E-RIPGREP-1 | macOS | acceptance | required | | | out of scope (platforms-windows-wsl) | |
| DH-E-RIPGREP-2 | Linux (WSL) | acceptance | required | E2 | | not run (stage not started) | |
| DH-E-RIPGREP-2 | Windows | acceptance | required | E2 | | not run (stage not started) | |
| DH-E-RIPGREP-2 | macOS | acceptance | required | | | out of scope (platforms-windows-wsl) | |
| DH-E-CHOKIDAR-1 | Linux (WSL) | acceptance | required | E2 | | not run (stage not started) | |
| DH-E-CHOKIDAR-1 | Windows | acceptance | required | E2 | | not run (stage not started) | |
| DH-E-CHOKIDAR-1 | macOS | acceptance | required | | | out of scope (platforms-windows-wsl) | |
| DH-E-CHOKIDAR-2 | Linux (WSL) | acceptance | required | E2 | | not run (stage not started) | |
| DH-E-CHOKIDAR-2 | Windows | acceptance | required | E2 | | not run (stage not started) | |
| DH-E-CHOKIDAR-2 | macOS | acceptance | required | | | out of scope (platforms-windows-wsl) | |
| DH-E-CONTEXT-2 | Linux (WSL) | acceptance | required | E2 | | not run (stage not started) | |
| DH-E-CONTEXT-2 | Windows | acceptance | required | E2 | | not run (stage not started) | |
| DH-E-STORAGE-6 | Linux (WSL) | acceptance | required | E2 | | not run (stage not started) | |
| DH-E-STORAGE-6 | Windows | acceptance | required | E2 | | not run (stage not started) | |
| DH-E-STORAGE-6 | macOS | acceptance | required | | | out of scope (platforms-windows-wsl) | |
| DH-E-NODEFS-1 | Linux guest | acceptance | required | E2 | | not run (stage not started) | |
| DH-E-NODEFS-1 | Windows guest | acceptance | required | E2 | | not run (stage not started) | |

## Manual checks register

None: impl.md DH-S-18 counts no manual check ("An acceptance step is ticked only by its scenarios").

| Step | Check | Seen | Operator | Commit | Label |
| --- | --- | --- | --- | --- | --- |

## Owner defaults (M2-C-4)

None: impl.md sec. 9.1 names no default to confirm before DH-S-18.

| Default | Raised in | Text (impl.md sec. 9.1) | Confirmed | By | Commit |
| --- | --- | --- | --- | --- | --- |

## Stage 8.2 blockers

implementation-plan Stage 8.2 (impl.md 18.1, `DH-E-TOOLCHAIN-2` first in S18, every later S18 step on
its binaries) was taken up on `fa1186d` and could not run. Nothing below is evidence of a run: both
TOOLCHAIN-2 rows stay `not run` and are owed, and so is every later S18 row, which needs the
rehearsal's binaries. Each open blocker on its own stops the run:

| Blocker | Seen | What clears it |
| --- | --- | --- |
| `harness-gate entry` exits 1: harness:DSH-M1 is not closed (DH-S-1 to DH-S-11 read `Closure status: open`; no `harness-DSH-M1/evidence/closure.md`). M2-DEC-1: no M2 scenario run before M1 closes, and each `s18` job runs `entry` first. | `GOWORK=off go -C tools/harness-gate run . entry --root ../..` on `fa1186d`: 12 blockers, exit 1 | M1 closure merges and `entry` exits 0 |
| The keyless replay clause has nothing to run. `TestToolchainRehearsal` runs `<dsh> headless --replay <dir>` on `src/harness/internal/testkit/scenarios/testdata/rehearsal-replay`, and its subtest fails (it never skips): the directory is not committed, and `dsh headless` has no `--replay` flag. The rehearsal's own seam `ReplayCheck` is nil, so `harness-gate rehearse` prints the clause `OPEN, not built`. The replay route of step 16.7 is reached through a profile layer (`llm.replay`, as `src/harness/test/replay` does), not a flag; impl.md step 16.11 (the replay gate) is in no DH-S-16 step row (proposal `replay-gate-step-dh-s-16`, open). | `dsh --home <scratch> headless --replay ...` built from `fa1186d`: `dsh headless: cli/usage: unknown flag --replay`, exit 64; `Test-Path` of the directory: false; `rehearse` on `fa1186d`: `replay of dsh-windows-amd64.exe: OPEN` | operator answer to `s18-rehearsal-replay`: a follow-up workstream retargets the replay subtest to the layer-based replay route (`llm.replay`) with a committed fixture |
| Who and where. TOOLCHAIN-2 runs on the `linux-wsl` variant (`self-hosted, linux, dsh-wsl`) and the `windows-hosted` variant (`windows-latest`) of [variants.md](variants.md), as the first scenario `harness-gate require --stage DH-S-18 --variant <label>` prints for each, on one merged commit. The Windows lane `win.ps1` runs no rehearsal; the Windows half runs only through the scenario. | `require --variant linux-wsl` and `--variant windows-hosted` on `fa1186d`: both lists start with `DH-E-TOOLCHAIN-2`, exit 0 | the `s18` jobs of `harness-scenarios.yml` (or the lanes' `--acceptance DH-S-18`) run on the merged commit once the blockers above clear |

Cleared on this stage's branch (they would have failed the scenario on every variant; the commit is
this stage's merge):

| Defect | Seen on `fa1186d` | Fix |
| --- | --- | --- |
| `TestToolchainRehearsal` wanted `dsh --version` to print exactly `dsh <version>`; `dsh` prints `dsh <version> (<revision>[-modified]; <go>; <os>/<arch>)` (`cli.versionLine`). | a build of `./cmd/dsh`: `dsh (devel) (fa1186d304d5...; go1.27.4; windows/amd64)` | the scenario parses the line and requires the recorded version, the recorded revision with no `-modified`, and the runner's platform |
| The rehearsal recorded `version=(devel)` (the binaries' build information) while each binary prints `0.1.4-SNAPSHOT-<sha7>`, which GoReleaser's default ldflags link in as `main.version`; `-trimpath` keeps `-ldflags` out of the build information. | `rehearse` on `fa1186d`: `version=(devel)`; the unpacked `dsh-windows-amd64.exe --version`: `dsh 0.1.4-SNAPSHOT-fa1186d (...)` | `.goreleaser.dsh.yaml` sets `ldflags: -s -w -X main.version={{ .Version }}` explicitly; `rehearse` records the version of the snapshot's `dist/metadata.json` and fails without one (`TestSnapshotVersion`); `TestRehearseSnapshotSixArchives` runs the host's binary and requires the recorded version |
| The rehearsal had no enforced place or hand-off: `harness-gate scenarios --stage DH-S-18` ran it in one `go test` run with every other S18 scenario, in the order `go test` found them, and `TestToolchainRehearsal` left its binaries in `t.TempDir()`, so later scenarios built `./cmd/dsh` again (`scenario.Bin` without `DSH_BIN`). | `go test -list` of the scenarios package: `TestDyingHostLeavesReadableReport` before `TestToolchainRehearsal`; `stageOrder` named only DH-S-17 | `stageOrder` runs `DH-E-TOOLCHAIN-2` alone first in DH-S-18 with `DSH_REHEARSAL_DIR`; the scenario writes its binaries there and `rehearsal.sha256` once every check passed; the runner runs the later phases with `DSH_BIN` on the host's binary after checking its SHA-256, and does not run them without one (`TestPhasesOrderDHS18`, `TestRehearsalBinary`, `TestScenarioGatesS18RehearsalHandoff`) |
| The s18 variant jobs bypassed that order: `harness-scenarios.yml` ran each variant's scenarios as one direct `go test -run "$RUN" ./...`, outside `harness-gate scenarios`, with neither `DSH_REHEARSAL_DIR` nor `DSH_BIN`, so `linux-wsl` and `windows-hosted` rehearsed in `go test` order into `t.TempDir()` and later scenarios rebuilt `./cmd/dsh`. | the s18 job's "Run the variant's scenarios" step | the step runs `harness-gate variant-run --stage DH-S-18 --scenarios "$LIST"` (the `require --variant` list, after `entry`): the same `stageOrder` phases and hand-off as `scenarios`, the manifest's checksums in the variant log; a variant without `DH-E-TOOLCHAIN-2` runs on the binary verified in `--rehearsal DIR` and runs nothing without one (`TestVariantRunS18Handoff`, `TestS18JobRunsVariantsThroughVariantRun`) |
| The s18 matrix did not carry the rehearsal across jobs: the variants without `DH-E-TOOLCHAIN-2` (`windows-host`, `two-account`, `share`, the Linux E2 jobs) had no rehearsed binary, and matrix entries of one job cannot wait on each other. | `variant-run --stage DH-S-18 --scenarios DH-E-STORE-1` with no `--rehearsal`: `not run`, exit 1 | `harness-scenarios.yml` job `s18-rehearsal` (`linux` on `dsh-wsl`, `windows` on `windows-latest`) runs `DH-E-TOOLCHAIN-2` first, after `entry`, through `variant-run --rehearsal`, and uploads the directory (a tar, keeping the binaries' mode) as `dsh-s18-rehearsal-<lane>`; `s18` `needs` it, and each variant without `DH-E-TOOLCHAIN-2` downloads its OS's directory and runs `variant-run --rehearsal` on it, its binary checked against `rehearsal.sha256` (`TestS18JobRunsVariantsThroughVariantRun`, `TestVariantRunS18Handoff`) |
| The repository gate failed at gate 2 (`vet`) on the base: QG-5 (a goroutine, listener or child process started in a test without a `t.Cleanup` release) in `internal/cli/{headless,sdk,second_launch,stop,terminal_linux}_test.go`, `internal/cli/lifecycle/lifecycle_test.go`, `internal/boot/{process_profiles,stop_inputs}_test.go` and `internal/testkit/scenarios/sdk_stdio_scenario_test.go`; QG-4 (`time.Sleep` in a test) in `internal/cli/{second_launch,terminal_linux}_test.go`, five scenario files and four DH-S-16 e2e files. | `go vet -vettool=<harness-gate>` with `-tags=`, `-tags=replay,scenario,bench`, `-tags=e2e` and `GOOS=linux`; the repaired tests pass on Windows and, with `-race`, on Linux (WSL: `internal/cli/...`, `internal/boot/...`, including both `secret set` pseudo-terminal tests, whose first repair waited in cleanup on a channel the interrupted test never closed) | each start registers a `t.Cleanup` that kills or stops it and joins it (the helper process of `process_profiles_test.go` ends in `os.Exit`: a reasoned `//lint:ignore cleanup`); each sleep is a ticker, a `procwait.Poll` or a wait on a signal (`dsh headless: turn` for the long turn), with the same bounds (operator answer to `s18-preexisting-vet-findings`) |

Probes, not evidence (M2-DEC-1; a modified tree): `harness-gate rehearse` on a Windows workstation at `fa1186d`
with this branch's edits built six archives, verified each against `checksums.txt` and extracted six
binaries (`vcs.modified=true`, so none can be recorded). On Linux the last WSL lane rehearsal record
([wsl-d26bb4b-20261126T0039Z-rehearse.txt](../../harness-DSH-M1/evidence/runs/wsl-d26bb4b-20261126T0039Z-rehearse.txt),
an M1 gate lane, not an S18 run) shows the same pipeline on `d26bb4b`.

When it runs, each TOOLCHAIN-2 row takes the run's commit, `E2 runner:<name>` (or `E2 wsl:<host>/<distro>`),
`passed` only if `TestToolchainRehearsal` passed with its replay subtest, and in Artifacts the
SHA-256 of the binary it executed and the run id. The six checksums go into
[Rehearsal binaries](#rehearsal-binaries-step-181) below, and every later S18 row records the SHA-256 of
the binary it ran, which must be one of them.

## Stage 8.4 blockers

implementation-plan Stage 8.4 (impl.md 18.3, `DH-E-EXEC-1` and `DH-E-EXEC-10` on team-kept VMs over
SSH, including the machine whose user manager is stopped) was taken up on `7b5133d` and could not run.
Nothing below is evidence of a run: both rows stay `not run` and are owed. A WSL result would count
only as the WSL variant (M2-R-05), so none is offered for the `Linux (E2)` rows. Each open blocker on
its own stops the run:

| Blocker | Seen | What clears it |
| --- | --- | --- |
| `harness-gate entry` exits 1: harness:DSH-M1 is not closed (M2-DEC-1), as in [Stage 8.2 blockers](#stage-82-blockers). | `GOWORK=off go -C tools/harness-gate run . entry --root ../..` on `7b5133d`: 12 blockers, exit 1 | M1 closure merges and `entry` exits 0 |
| No rehearsed binary: the `linux-e2:*` jobs of `harness-scenarios.yml` run on the `s18-rehearsal` job's directory, which Stage 8.2 left unbuilt (its replay blocker is open). | [Stage 8.2 blockers](#stage-82-blockers) | Stage 8.2's blockers clear and `s18-rehearsal` uploads `dsh-s18-rehearsal-linux` |
| No lab: the repository has no `dsh-m2-s18` environment, so the variables `DSH_SCENARIO_EXEC1_*` and `DSH_SCENARIO_EXEC10_*` (the VM hosts, including `DSH_SCENARIO_EXEC10_SSH_NOMANAGER`) and the secrets `DSH_LAB_SSH_KEY` and `DSH_LAB_KNOWN_HOSTS` are unset; no team-kept VM is reachable from the workstation that took the stage up (the two SSH hosts it knows are personal machines and time out). | `gh api repos/smartpcr/agent-plugin/environments/dsh-m2-s18/variables`: HTTP 404; `ssh -o ConnectTimeout=10 <host> true` for both hosts: timed out | operator answer to `s18-exec-lab-machines`: the environment, the delegated cgroup v2 VM, the VM with its user manager stopped, and a scratch account on each |
| The product does not say what both scenarios read (docs/harness/areas/execution.md, DH-E-EXEC-1 and DH-E-EXEC-10). The bash tool's result (`shellexec/tools.Result`) has no owner, limits or survivors field, so "owner: cgroup" and "limits: enforced" cannot appear; the owner kinds are named `control-group`, `job-object`, `process-group` (`subprocess.ControlGroup` and siblings), not `cgroup`/`job`; no memory limit reaches a tree (`shellexec.Executor.Start` builds `subprocess.Spec` without `Limits`; neither the bash input nor `bash.Config` carries one; only `subprocess/limits.go` writes `memory.max`); the host announces its owner only on a fallback (`boot/steps.go`), never the delegated scope (`subprocess.DelegatedScope` is read only by tests), and that line has no `warn` word, which both scenarios' fallback pattern requires; no CLI path runs the bash tool without a model turn. These belong to build stage DH-S-6 and are past this step's boundary. | reading the code on `7b5133d` against the scenarios' assertions | operator answer to `s18-exec-product-gaps` |
| `TestLinuxWholeProcessTreeIsKilled` (DH-E-EXEC-1) checks the runner itself (local `/proc`, cgroup directories, fixtures) and needs a container for its no-cgroup clause, while the scenario names a delegated cgroup v2 VM over SSH: on `linux-e2:*` it would test `dsh-e2-controller`, not the VM. | `src/harness/internal/testkit/scenarios/exec_tree_scenario_test.go` on `7b5133d` | operator answer to `s18-exec1-over-ssh` |

Cleared on this stage's branch (it would have failed the scenario on every variant; the commit is
this stage's merge):

| Defect | Seen on `7b5133d` | Fix |
| --- | --- | --- |
| `TestLinuxSSHLoginDelegatedScopeOwnsTree` (DH-E-EXEC-10) set `GOOS`/`GOARCH` before `scenario.Bin`, which then ignores `DSH_BIN` and cross-builds `./cmd/dsh`: the VM never ran the rehearsal's binary (impl.md 18.1), and the row could record no manifest SHA-256. | `scenario.Bin` returns `DSH_BIN` only when no build variable is set | the scenario copies `dsh-linux-<arch>` (the VM's `uname -m`) from the rehearsal directory beside `DSH_BIN`, checked against `rehearsal.sha256`, and logs its SHA-256; with no manifest it uses `DSH_BIN` only when the runner is `linux/<arch>`, and fails otherwise; without `DSH_BIN` (development) it cross-builds as before (`exec10Binary`, `TestExec10PickRehearsalBinary`) |

When they run, the EXEC-1 row takes the run's commit, `E2 runner:<name>` and the variant's preflight
(`systemctl --user` state, cgroup v2 and delegation), and the EXEC-10 row the same for each VM, the
no-user-manager VM's row reporting the process-group fallback; Artifacts carry the SHA-256 of the
binary the VM ran (one of the [Rehearsal binaries](#rehearsal-binaries-step-181)) and the run id.

## Evidence record (impl.md sec. 7.3)

### Source and build identity

Not yet recorded. At closure: the commit of the three modules (one commit), the Go version (`go1.27.4`),
the binary's SHA-256 and embedded version.

#### Rehearsal binaries (step 18.1)

The binaries every S18 step runs (impl.md DH-S-18 Sequence), as the TOOLCHAIN-2 run's
`harness-gate rehearse` lines print them. Not yet rehearsed: see [Stage 8.2 blockers](#stage-82-blockers).

| Binary | SHA-256 | Version | vcs.revision | Executed on |
| --- | --- | --- | --- | --- |
| dsh-linux-amd64 | | | | |
| dsh-linux-arm64 | | | | |
| dsh-darwin-amd64 | | | | |
| dsh-darwin-arm64 | | | | |
| dsh-windows-amd64.exe | | | | |
| dsh-windows-arm64.exe | | | | |

### Environment and fixture

Not yet recorded. Per row of the Scenarios table: environment id (tech-spec sec. 4.3), operating system
and version, sandbox backend and reported enforcement, versions of external programs, fixture.

### Exact tests and commands

Not yet recorded: the gate runner's output, the `go test` command lines and `DSH_REQUIRE_SCENARIOS`
(`harness-gate require --stage DH-S-18`).

### Run identifiers

Not yet recorded: CI run and job ids, or the local run-record id and its SHA-256 (M1 runbook template);
session ids and provider request ids for model calls.

### Results

The Scenarios table above, and the local test results per step at closure. Not yet recorded.

### Manual checks

The Manual checks register above.

### Cleanup evidence

Not yet recorded: the before-and-after comparison the stage names, or what was retained.

### Open items

Manual checks: none count here (impl.md DH-S-18).
