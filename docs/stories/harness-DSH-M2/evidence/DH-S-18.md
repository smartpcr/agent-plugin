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
| 18.2 | DH-E-HOST-4 | | not run; blocked, see [Stage 8.3 blockers](#stage-83-blockers) |
| 18.3 | DH-E-EXEC-1, DH-E-EXEC-10 | | not run; blocked, see [Stage 8.4 blockers](#stage-84-blockers) |
| 18.4 | DH-E-NATIVESYSTEM-1 to -4, DH-E-SANDBOX-1 | | not run; blocked, see [Stage 8.5 confinement runs](#stage-85-confinement-runs-step-184) |
| 18.5 | DH-E-FS-1, DH-E-FS-2 | | not run; blocked, see [Stage 8.6 blockers](#stage-86-blockers) |
| 18.6 | DH-E-VIEW-5 | | not run; blocked, see [Stage 8.6 blockers](#stage-86-blockers) |
| 18.7 | DH-E-RIPGREP-1, DH-E-RIPGREP-2 | | not run; blocked, see [Stage 8.7 blockers](#stage-87-blockers) |
| 18.8 | DH-E-CHOKIDAR-1, DH-E-CHOKIDAR-2 | | not run; blocked, see [Stage 8.7 blockers](#stage-87-blockers) |
| 18.9 | DH-E-CONTEXT-2 | | not run; blocked, see [Stage 8.8 blockers](#stage-88-blockers) |
| 18.10 | DH-E-STORAGE-6 | | not run; blocked, see [Stage 8.8 blockers](#stage-88-blockers) |
| 18.11 | DH-E-NODEFS-1 | | run last; not run, see [Stage 8.9 blockers](#stage-89-blockers) |

## Scenarios

The 19 scenarios of the steps, one row per operating-system variant (M2-DOD-4). A variant in scope but not run keeps the stage open; macOS rows read out of scope by decision `platforms-windows-wsl` (M2-EN-2). Each row's variant is a variant of [variants.md](variants.md); the first rows of a scenario keep the labels the record was created with (Stage 1.1), which the gate owes, and read as follows. `Linux (WSL)` is `linux-wsl` (`linux-wsl:smb` for CHOKIDAR-2, `linux-wsl:two-account` for STORAGE-6); `Windows` is `windows-hosted` (`windows-host` for FS-2, `windows-host:smb` for CHOKIDAR-2, `windows-host:two-account` for STORAGE-6); `Linux ext4` is `linux-wsl` and `Windows NTFS` is `windows-hosted`; `Linux guest` and `Windows guest` are `vm:linux-guest` and `vm:windows-guest`. A `Linux (E2)` row (`Linux (E2, over SSH)` for EXEC-10) stands for the shared machine set of its scenario: it reads `passed` only when every `linux-e2:*` row of that scenario below it passed in one workflow run (the variants.md recording rule). Every other variant of variants.md has its own row, labelled as there. Reported enforcement (`Full` or `Partial`, with each exposure) is copied from each run into [Stage 8.5 confinement runs](#stage-85-confinement-runs-step-184), which the confinement rows' Artifacts name.

| Scenario | Variant | Venue | Leg | Environment | Commit | Result | Artifacts |
| --- | --- | --- | --- | --- | --- | --- | --- |
| DH-E-TOOLCHAIN-2 | Linux (WSL) | acceptance | required | E2 | | not run (harness-gate entry fails: harness:DSH-M1 not closed, M2-DEC-1) | |
| DH-E-TOOLCHAIN-2 | Windows | acceptance | required | E2 | | not run (harness-gate entry fails: harness:DSH-M1 not closed, M2-DEC-1) | |
| DH-E-TOOLCHAIN-2 | macOS | acceptance | required | | | out of scope (platforms-windows-wsl) | |
| DH-E-HOST-4 | Linux (WSL) | acceptance | required | E2 | | not run (harness-gate entry fails: harness:DSH-M1 not closed, M2-DEC-1; no crash component, see Stage 8.3 blockers) | |
| DH-E-HOST-4 | Windows | acceptance | required | E2 | | not run (harness-gate entry fails: harness:DSH-M1 not closed, M2-DEC-1; no crash component, see Stage 8.3 blockers) | |
| DH-E-HOST-4 | macOS | acceptance | required | | | out of scope (platforms-windows-wsl) | |
| DH-E-EXEC-1 | Linux (E2) | acceptance | required | E2 | | not run (harness-gate entry fails: harness:DSH-M1 not closed, M2-DEC-1) | per machine: the `linux-e2:systemd` and `linux-e2:no-user-manager` rows below |
| DH-E-EXEC-1 | linux-e2:systemd | acceptance | required | E2 | | not run (harness-gate entry fails: harness:DSH-M1 not closed, M2-DEC-1; see [Stage 8.4 blockers](#stage-84-blockers)) | |
| DH-E-EXEC-1 | linux-e2:no-user-manager | acceptance | required | E2 | | not run (harness-gate entry fails: harness:DSH-M1 not closed, M2-DEC-1; see [Stage 8.4 blockers](#stage-84-blockers)) | |
| DH-E-EXEC-10 | Linux (E2, over SSH) | acceptance | required | E2 | | not run (harness-gate entry fails: harness:DSH-M1 not closed, M2-DEC-1) | per machine: the `linux-e2:systemd` and `linux-e2:no-user-manager` rows below |
| DH-E-EXEC-10 | linux-e2:systemd | acceptance | required | E2 | | not run (harness-gate entry fails: harness:DSH-M1 not closed, M2-DEC-1; see [Stage 8.4 blockers](#stage-84-blockers)) | |
| DH-E-EXEC-10 | linux-e2:no-user-manager | acceptance | required | E2 | | not run (harness-gate entry fails: harness:DSH-M1 not closed, M2-DEC-1; see [Stage 8.4 blockers](#stage-84-blockers)) | |
| DH-E-NATIVESYSTEM-1 | Linux (E2) | acceptance | required | E2 | | not run (harness-gate entry fails: harness:DSH-M1 not closed, M2-DEC-1) | per machine: [Stage 8.5 confinement runs](#stage-85-confinement-runs-step-184) |
| DH-E-NATIVESYSTEM-1 | linux-e2:landlock | acceptance | required | E2 | | not run (harness-gate entry fails: harness:DSH-M1 not closed, M2-DEC-1) | per machine: [Stage 8.5 confinement runs](#stage-85-confinement-runs-step-184) |
| DH-E-NATIVESYSTEM-2 | Linux (E2) | acceptance | required | E2 | | not run (harness-gate entry fails: harness:DSH-M1 not closed, M2-DEC-1) | per machine: [Stage 8.5 confinement runs](#stage-85-confinement-runs-step-184) |
| DH-E-NATIVESYSTEM-2 | linux-e2:landlock-abi1-4 | acceptance | required | E2 | | not run (harness-gate entry fails: harness:DSH-M1 not closed, M2-DEC-1) | per machine: [Stage 8.5 confinement runs](#stage-85-confinement-runs-step-184) |
| DH-E-NATIVESYSTEM-3 | Linux (E2) | acceptance | required | E2 | | not run (harness-gate entry fails: harness:DSH-M1 not closed, M2-DEC-1) | per machine: [Stage 8.5 confinement runs](#stage-85-confinement-runs-step-184) |
| DH-E-NATIVESYSTEM-3 | linux-e2:unconfined | acceptance | required | E2 | | not run (harness-gate entry fails: harness:DSH-M1 not closed, M2-DEC-1) | per machine: [Stage 8.5 confinement runs](#stage-85-confinement-runs-step-184) |
| DH-E-NATIVESYSTEM-3 | linux-e2:landlock | acceptance | required | E2 | | not run (harness-gate entry fails: harness:DSH-M1 not closed, M2-DEC-1) | per machine: [Stage 8.5 confinement runs](#stage-85-confinement-runs-step-184) |
| DH-E-NATIVESYSTEM-4 | Linux (E2) | acceptance | required | E2 | | not run (harness-gate entry fails: harness:DSH-M1 not closed, M2-DEC-1) | per machine: [Stage 8.5 confinement runs](#stage-85-confinement-runs-step-184) |
| DH-E-NATIVESYSTEM-4 | linux-e2:landlock | acceptance | required | E2 | | not run (harness-gate entry fails: harness:DSH-M1 not closed, M2-DEC-1) | per machine: [Stage 8.5 confinement runs](#stage-85-confinement-runs-step-184) |
| DH-E-NATIVESYSTEM-4 | linux-e2:musl | acceptance | required | E2 | | not run (harness-gate entry fails: harness:DSH-M1 not closed, M2-DEC-1) | per machine: [Stage 8.5 confinement runs](#stage-85-confinement-runs-step-184) |
| DH-E-SANDBOX-1 | Linux (E2) | acceptance | required | E2 | | not run (harness-gate entry fails: harness:DSH-M1 not closed, M2-DEC-1) | per machine: [Stage 8.5 confinement runs](#stage-85-confinement-runs-step-184) |
| DH-E-SANDBOX-1 | linux-e2:bwrap | acceptance | required | E2 | | not run (harness-gate entry fails: harness:DSH-M1 not closed, M2-DEC-1) | per machine: [Stage 8.5 confinement runs](#stage-85-confinement-runs-step-184) |
| DH-E-SANDBOX-1 | linux-e2:landlock | acceptance | required | E2 | | not run (harness-gate entry fails: harness:DSH-M1 not closed, M2-DEC-1) | per machine: [Stage 8.5 confinement runs](#stage-85-confinement-runs-step-184) |
| DH-E-SANDBOX-1 | linux-e2:unconfined | acceptance | required | E2 | | not run (harness-gate entry fails: harness:DSH-M1 not closed, M2-DEC-1) | per machine: [Stage 8.5 confinement runs](#stage-85-confinement-runs-step-184) |
| DH-E-FS-1 | Linux ext4 | acceptance | required | E2 | | not run (harness-gate entry fails: harness:DSH-M1 not closed, M2-DEC-1) | |
| DH-E-FS-1 | Windows NTFS | acceptance | required | E2 | | not run (harness-gate entry fails: harness:DSH-M1 not closed, M2-DEC-1) | |
| DH-E-FS-1 | linux-wsl:smb | acceptance | required | E2 | | not run (harness-gate entry fails: harness:DSH-M1 not closed, M2-DEC-1) | |
| DH-E-FS-1 | windows-host:smb | acceptance | required | E2 | | not run (harness-gate entry fails: harness:DSH-M1 not closed, M2-DEC-1) | |
| DH-E-FS-1 | macOS | acceptance | required | | | out of scope (platforms-windows-wsl) | |
| DH-E-FS-2 | Linux (WSL) | acceptance | required | E2 | | not run (harness-gate entry fails: harness:DSH-M1 not closed, M2-DEC-1) | |
| DH-E-FS-2 | Windows | acceptance | required | E2 | | not run (harness-gate entry fails: harness:DSH-M1 not closed, M2-DEC-1) | |
| DH-E-FS-2 | windows-host:smb | acceptance | required | E2 | | not run (harness-gate entry fails: harness:DSH-M1 not closed, M2-DEC-1) | |
| DH-E-FS-2 | macOS | acceptance | required | | | out of scope (platforms-windows-wsl) | |
| DH-E-VIEW-5 | Linux (WSL) | acceptance | required | E2 | | not run (harness-gate entry fails: harness:DSH-M1 not closed, M2-DEC-1) | |
| DH-E-RIPGREP-1 | Linux (WSL) | acceptance | required | E2 | | not run (harness-gate entry fails: harness:DSH-M1 not closed, M2-DEC-1) | |
| DH-E-RIPGREP-1 | Windows | acceptance | required | E2 | | not run (harness-gate entry fails: harness:DSH-M1 not closed, M2-DEC-1) | |
| DH-E-RIPGREP-1 | macOS | acceptance | required | | | out of scope (platforms-windows-wsl) | |
| DH-E-RIPGREP-2 | Linux (WSL) | acceptance | required | E2 | | not run (harness-gate entry fails: harness:DSH-M1 not closed, M2-DEC-1) | |
| DH-E-RIPGREP-2 | Windows | acceptance | required | E2 | | not run (harness-gate entry fails: harness:DSH-M1 not closed, M2-DEC-1) | |
| DH-E-RIPGREP-2 | macOS | acceptance | required | | | out of scope (platforms-windows-wsl) | |
| DH-E-CHOKIDAR-1 | Linux (WSL) | acceptance | required | E2 | | not run (harness-gate entry fails: harness:DSH-M1 not closed, M2-DEC-1) | |
| DH-E-CHOKIDAR-1 | Windows | acceptance | required | E2 | | not run (harness-gate entry fails: harness:DSH-M1 not closed, M2-DEC-1) | |
| DH-E-CHOKIDAR-1 | macOS | acceptance | required | | | out of scope (platforms-windows-wsl) | |
| DH-E-CHOKIDAR-2 | Linux (WSL) | acceptance | required | E2 | | not run (harness-gate entry fails: harness:DSH-M1 not closed, M2-DEC-1) | |
| DH-E-CHOKIDAR-2 | Windows | acceptance | required | E2 | | not run (harness-gate entry fails: harness:DSH-M1 not closed, M2-DEC-1) | |
| DH-E-CHOKIDAR-2 | macOS | acceptance | required | | | out of scope (platforms-windows-wsl) | |
| DH-E-CONTEXT-2 | Linux (WSL) | acceptance | required | E2 | | not run (blocked: harness-gate entry fails, M2-DEC-1; the release build has no instruction-file component and no route, see Stage 8.8 blockers) | |
| DH-E-CONTEXT-2 | Windows | acceptance | required | E2 | | not run (blocked: harness-gate entry fails, M2-DEC-1; the release build has no instruction-file component and no route, see Stage 8.8 blockers) | |
| DH-E-CONTEXT-2 | macOS | acceptance | required | | | out of scope (platforms-windows-wsl) | |
| DH-E-STORAGE-6 | Linux (WSL) | acceptance | required | E2 | | not run (blocked: harness-gate entry fails, M2-DEC-1; dsh cannot seed an attachment or a spill file, see Stage 8.8 blockers) | |
| DH-E-STORAGE-6 | Windows | acceptance | required | E2 | | not run (blocked: harness-gate entry fails, M2-DEC-1; dsh cannot seed an attachment or a spill file, see Stage 8.8 blockers) | |
| DH-E-STORAGE-6 | macOS | acceptance | required | | | out of scope (platforms-windows-wsl) | |
| DH-E-NODEFS-1 | Linux guest | acceptance | required | E2 | | not run (harness-gate entry fails: harness:DSH-M1 not closed, M2-DEC-1) | |
| DH-E-NODEFS-1 | Windows guest | acceptance | required | E2 | | not run (harness-gate entry fails: harness:DSH-M1 not closed, M2-DEC-1) | |
| DH-E-NODEFS-1 | vm:macos-guest | acceptance | required | | | out of scope (platforms-windows-wsl) | |

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

## Stage 8.3 blockers

implementation-plan Stage 8.3 (impl.md 18.2, `DH-E-HOST-4` on Linux and Windows; "reaches the UI" is
the readiness probe answering until S22, M2-DEC-6) was taken up on `7b5133d` and could not run.
Nothing below is evidence of a run: both HOST-4 rows stay `not run` and are owed. Each open blocker on
its own stops the run:

| Blocker | Seen | What clears it |
| --- | --- | --- |
| `harness-gate entry` exits 1: harness:DSH-M1 is not closed (M2-DEC-1), as for [Stage 8.2](#stage-82-blockers). | `GOWORK=off go -C tools/harness-gate run . entry --root ../..` on `7b5133d`: 12 blockers, exit 1 | M1 closure merges and `entry` exits 0 |
| No rehearsed binary: every later S18 step runs on the binaries of `DH-E-TOOLCHAIN-2` (impl.md DH-S-18 Sequence), and `variant-run` runs nothing without a verified `--rehearsal` directory. | Stage 8.2's rows read `not run` | Stage 8.2's blockers clear and its rehearsal runs on the merged commit |
| The crash component of the Given does not exist. impl.md DH-E-HOST-4 names "a test-only component, enabled by configuration, that can fail at load, panic in a goroutine, or trigger a runtime fault on request"; no such component is built, so `DSH_SCENARIO_HOST4_LAYER` has nothing to enable, and the `load`, `panic`, `fault` and twelve-failure legs fail on every variant. A goroutine panic or a runtime fault also never reaches the fatal path today: only `boot.Run`'s own goroutine recovers into `diag.Fatal`, and nothing uses `diag.Key`, so the Go runtime prints the panic (and `<home>/run/crash.txt`) and standard error names no report, which the legs require. | a search of `src/harness/internal` for a crash component and for `diag.Key` consumers: none outside `boot/steps.go`'s provider | operator answer to `s18-host4-crash-component`: a follow-up workstream builds the test-only component (its goroutine recovering into `diag.Fatal`, `debug.SetPanicOnFault` for the fault) behind a build tag or a configuration key that no shipped profile sets |
| The run-time inputs are unset: the repository holds no `DSH_SCENARIO_HOST4_ARGS`, `_READY` or `_LAYER` variable, which the `s18` job passes from `vars`. | `gh variable list` on this repository: none of the three | set `DSH_SCENARIO_HOST4_ARGS=web;--no-open;--port;0` and `DSH_SCENARIO_HOST4_READY={bin} --home {home} status` (the values of the probes below; `sh -c` and `cmd /C` both run it), and `_LAYER` once the component exists |

Cleared on this stage's branch (they would have failed the scenario on every variant or left the plan's
Given unproved; the commit is this stage's merge):

| Defect | Seen on `7b5133d` | Fix |
| --- | --- | --- |
| The scenario had no killed host. implementation-plan Stage 8.3's Given is "a killed host", and TC-11b's crash is SIGKILL / `TerminateProcess`; `TestDyingHostLeavesReadableReport` only ran the crash component's three failures, each of which the host survives long enough to report itself. | `host_report_scenario_test.go`: no leg kills the host | a `killed` leg runs first: the host answers the readiness probe, its tree is killed with the forced rung alone (`Process.Kill`: SIGKILL / `TerminateProcess`, no SIGTERM or console close first, so the host cannot remove its record), the wait for its exit is bounded at a minute, and the next start must print the unclean-exit line naming a report under `<home>/reports` with `--safe`; the report must be owner-only (`home.CheckPrivateFile`) and name the unclean exit of the killed pid |
| A missing layer failed the whole scenario before any leg ran: `need(DSH_SCENARIO_HOST4_LAYER)` was the first line, so neither the safe-mode leg nor a killed host could be seen without the crash component. | a run without `_LAYER`: `DSH_SCENARIO_HOST4_LAYER is not set`, no subtest run | the layer is read by the legs that use it (`load`, `panic`, `fault`, twelve failures), which still fail without it; the scenario still fails, never skips (DH-O-15) |
| On Windows the web host could not start on the scenario's home: `t.TempDir()` under the test's long name put `<home>/run/web-<id>.sock` past the Unix socket path bound (108 bytes), and `host.launch` failed `bind: invalid argument`, so no start ever answered. | Windows, `-run TestDyingHostLeavesReadableReport`: `listen unix C:\Users\...\TestDyingHostLeavesReadableReport45692014\001\home\run\web-919f83cf77ce.sock: bind: invalid argument`; `killed` and `safe mode` timed out after 30 s | the home lives under `os.MkdirTemp("", "dsh-h4-")` (`host4Dir`), as `internal/boot`'s `shortHome` does; removal is retried for ten seconds while a killed process lets go of its files |
| The readiness probe could not name the binary under test, so an operator had to hard-code a path that `DSH_BIN` or the rehearsal hand-off changes. | `DSH_SCENARIO_HOST4_READY` expanded only `{home}` | `{bin}` expands to the binary the scenario runs |

Probes, not evidence (M2-DEC-1; a modified tree): with `DSH_SCENARIO_HOST4_ARGS=web;--no-open;--port;0`,
`DSH_SCENARIO_HOST4_READY={bin} --home {home} status` and no `_LAYER`, `go test -tags scenario -run
TestDyingHostLeavesReadableReport ./internal/testkit/scenarios/` on this branch's edits over `7b5133d`
passed `killed` and `safe mode reaches the UI` and failed the four crash-component legs on
`DSH_SCENARIO_HOST4_LAYER is not set`, on Windows native (a Windows workstation, scenario build
`3017edcf...`) and on Ubuntu 24.04 under WSL2 (kernel `6.18.58.1-microsoft-standard-WSL2`, `DSH_BIN` built
with `-buildvcs=false` from the Windows checkout, scenario build `997f1512...`). By hand on Windows, the
restart after `TerminateProcess` printed `dsh: the host of http://127.0.0.1:64675 (process 29296) did not
stop cleanly; report: <home>\reports\report-20261128T063346.772282000Z-29296.txt; start with --safe (dsh
--safe web) ...`, the report's ACL held only the user, SYSTEM and Administrators, and `dsh --safe web` was
`live` in `dsh status`.

When it runs, each HOST-4 row takes the run's commit, `E2 runner:<name>` (or `E2 wsl:<host>/<distro>`),
`passed` only if every leg of `TestDyingHostLeavesReadableReport` passed, and in Artifacts the SHA-256 of
the binary it ran (one of the [rehearsal binaries](#rehearsal-binaries-step-181)) and the run id.

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

Cleared on this stage's branch (each would have failed its scenario on every variant; the commit is
this stage's merge):

| Defect | Seen on `7b5133d` | Fix |
| --- | --- | --- |
| `TestLinuxSSHLoginDelegatedScopeOwnsTree` (DH-E-EXEC-10) set `GOOS`/`GOARCH` before `scenario.Bin`, which then ignores `DSH_BIN` and cross-builds `./cmd/dsh`: the VM never ran the rehearsal's binary (impl.md 18.1), and the row could record no manifest SHA-256. | `scenario.Bin` returns `DSH_BIN` only when no build variable is set | the scenario copies `dsh-linux-<arch>` (the VM's `uname -m`) from the rehearsal directory beside `DSH_BIN`, checked against `rehearsal.sha256`, and logs its SHA-256; with no manifest (absent, not unreadable: an unreadable one fails) it uses `DSH_BIN` only when the runner is `linux/<arch>`, and fails otherwise; without `DSH_BIN` (development) it cross-builds as before (`exec10Binary`, `TestExec10PickRehearsalBinary`) |
| `TestLinuxWholeProcessTreeIsKilled` (DH-E-EXEC-1) checked the runner itself (local `/proc`, cgroup directories, fixtures, the binary built on the runner) and ran its no-cgroup clause in a container, while impl.md 18.3 runs it on the team-kept VMs over SSH: on `linux-e2:*` it would have tested `dsh-e2-controller`, not the VM. | `exec_tree_scenario_test.go` on `7b5133d`: `os.ReadFile("/proc/<pid>/stat")`, `os.Stat` of each cgroup, `DSH_SCENARIO_EXEC1_NOCGROUP` | every clause runs on the VM over SSH, on the machine set variants.md gives EXEC-1 and EXEC-10 (`DSH_SCENARIO_EXEC10_SSH` for clauses 1 and 2, `DSH_SCENARIO_EXEC10_SSH_NOMANAGER`, no writable cgroup and no user manager, for clause 3): the VM's binary (`exec10Binary`), fixtures, pids, cgroups and log; `DSH_SCENARIO_EXEC1_START` runs detached from the SSH session; `DSH_SCENARIO_EXEC1_NOCGROUP` is no longer read (the s18 job still passes it; harmless) |
| Every cleanup of both scenarios ran its SSH commands on `t.Context()`, which `testing` cancels just before the cleanups run: stopping the host, the leftover-scope check and the scratch removal would each have failed at once. | `exec10Machine.ssh` on `7b5133d` | a cleanup's command gets a fresh context bounded by two minutes (`exec10Context`, `TestExec10ContextLiveInCleanup`) |
| EXEC-1's detached `nohup` start (the SIGKILL clause) entered the cleanup's kill list only after both fixture pids and `dsh.pid` were read: a launch that never became ready failed the clause and left dsh and its partial tree running on the VM. The synchronous clauses also dropped the pids they had found when one was missing. | `exec_tree_scenario_test.go` in this stage | `exec1Launch` starts the line under `setsid`, records the session leader in `start.pid`, and registers a sweep before any wait. The sweep kills the session's group plus every pid file of the step and `dsh.pid`, read on the target. `exec1Wait` records found pids before failing. Covered by `TestExec1LaunchSweepsHalfReadyLaunch` (portable) and `TestExec1SweepKillsHalfReadyLaunch` (Linux, real `sh`, passed under WSL). That test also caught dash's `kill` rejecting `--`. |

When they run, the EXEC-1 row takes the run's commit, `E2 runner:<name>` and the variant's preflight
(`systemctl --user` state, cgroup v2 and delegation), and the EXEC-10 row the same for each VM, the
no-user-manager VM's row reporting the process-group fallback; Artifacts carry the SHA-256 of the
binary the VM ran (one of the [Rehearsal binaries](#rehearsal-binaries-step-181)) and the run id.

## Stage 8.5 confinement runs (step 18.4)

implementation-plan Stage 8.5 (impl.md 18.4) refines the five `Linux (E2)` rows above into one row per
machine of [variants.md](variants.md). A `Linux (E2)` row reads `passed` only when every machine row of
its scenario below passed in one workflow run on the merged commit (the variants.md recording rule for
shared machine sets); its Artifacts then take the run id. Each row copies the reported enforcement from
the run's log, never from the preflight:

- **Status**: the `<alias>: enforcement <State> <backend>, missing [..], exposed [..]` line, the head of
  `dsh sandbox status` on that machine (`sandboxVerdict`).
- **Denied write**: the `<alias>: enforcement ...` (SANDBOX-1) or `<alias>: denied write ...`
  (NATIVESYSTEM-1, -2) line, `<state>, <backend>, exit <n>, missing [..], exposed [..]`, the result of
  the confined write outside the roots, with its stderr; for a refused start, the `refused` line.

| Scenario | Variant | Preflight | Commit | Result | Status | Denied write |
| --- | --- | --- | --- | --- | --- | --- |
| DH-E-SANDBOX-1 | `linux-e2:bwrap` | bwrap Full | | not run (blocked, below) | | |
| DH-E-SANDBOX-1 | `linux-e2:landlock` | landlock ABI >= 5 | | not run (blocked, below) | | |
| DH-E-SANDBOX-1 | `linux-e2:unconfined` | Unconfined | | not run (blocked, below) | | |
| DH-E-NATIVESYSTEM-1 | `linux-e2:landlock` | landlock ABI >= 5 | | not run (blocked, below) | | |
| DH-E-NATIVESYSTEM-2 | `linux-e2:landlock-abi1-4` | landlock ABI 1-4 | | not run (blocked, below) | | |
| DH-E-NATIVESYSTEM-3 | `linux-e2:unconfined` (Landlock disabled) | Unconfined | | not run (blocked, below) | | |
| DH-E-NATIVESYSTEM-3 | `linux-e2:landlock` (missing grant) | landlock ABI >= 5 | | not run (blocked, below) | | |
| DH-E-NATIVESYSTEM-4 | `linux-e2:landlock` (pure Go) | landlock ABI >= 5 | | not run (blocked, below) | | |
| DH-E-NATIVESYSTEM-4 | `linux-e2:landlock` (cgo `-race`) | landlock ABI >= 5 | | not run (blocked, below) | | |
| DH-E-NATIVESYSTEM-4 | `linux-e2:musl` | landlock ABI >= 5 | | not run (blocked, below) | | |

Taken up on `7b5133d`; nothing above is evidence of a run. Each open blocker on its own stops it:

| Blocker | Seen | What clears it |
| --- | --- | --- |
| `harness-gate entry` exits 1: harness:DSH-M1 is not closed (M2-DEC-1), as for [Stage 8.2](#stage-82-blockers). | `GOWORK=off go -C tools/harness-gate run . entry --root ../..` on `7b5133d`: 12 blockers, exit 1 | M1 closure merges and `entry` exits 0 |
| No rehearsed binary: step 18.1 has not run, so there is no `rehearsal.sha256` for `variant-run --rehearsal` to check the `linux-e2:*` jobs' binary against (impl.md DH-S-18 Sequence). | [Rehearsal binaries](#rehearsal-binaries-step-181): empty | the `s18-rehearsal` job passes on the merged commit |
| The machines: SANDBOX-1 needs the `bwrap=`, `landlock=` and `unconfined=` machines of `DSH_SCENARIO_SANDBOX1_MACHINES` in one test, NATIVESYSTEM-2 and -4 add `landlock-abi1-4=` and `musl=`; each is a team-kept VM over SSH on `dsh-e2-controller` (Q `m2-e2-fleet`). | the workstation that took the stage up has no SSH alias for any `linux-e2:*` machine and `DSH_SCENARIO_SANDBOX1_MACHINES` is unset | the team's five VMs pass their preflights on the `s18` `linux-e2:*` jobs |

Cleared on this stage's branch (it would have let a wrong machine pass):

| Defect | Seen on `7b5133d` | Fix |
| --- | --- | --- |
| SANDBOX-1 took a machine's status as matching when the report text contained the backend's name anywhere; the report lists every backend of the chain, so the bwrap machine (`landlock: available`) passed as the landlock machine and any Linux machine (`bwrap: unavailable: ...`) passed as the bwrap machine. NATIVESYSTEM-1 and -2 did not read status at all, SANDBOX-1 (c) and NATIVESYSTEM-3 (a) checked only the word `Unconfined`, and no result line carried the exposures the step copies. | `dsh sandbox status` of the pure-Go build under WSL2 (a probe, not evidence: `linux-wsl`, not `linux-e2:bwrap`): `state: Full`, `backend: bwrap`, `missing: none`, `exposed: none`, then `bwrap: available ...` and `landlock: available ...` | the scenarios read the report's head (`readVerdict`): bwrap `Full`; landlock `Full` or `Partial` naming a missing effect in SANDBOX-1, and `Partial` in NATIVESYSTEM-1 to -4 (status judges a network-denied start, `sandbox.Expect`, and Landlock governs no network on any ABI, so `network` is named missing; the ABI is told apart by each start's own record), NATIVESYSTEM-3 (b) read before its grant is removed; the unconfined machine through `mustStatusUnconfined`; the verdict and each denied write are logged with missing and exposed effects (`TestVerdictIsTheReportHead`) |

## Stage 8.6 blockers

implementation-plan Stage 8.6 (impl.md 18.5 and 18.6: `DH-E-FS-1`, `DH-E-FS-2`, `DH-E-VIEW-5`) was taken
up on `7b5133d` and could not run. Nothing below is evidence of a run: the FS-1, FS-2 and VIEW-5 rows stay
`not run` and are owed, on `linux-wsl`, `linux-wsl:smb`, `windows-hosted`, `windows-host` and
`windows-host:smb` of [variants.md](variants.md). Each open blocker on its own stops the run:

| Blocker | Seen | What clears it |
| --- | --- | --- |
| `harness-gate entry` exits 1: harness:DSH-M1 is not closed (M2-DEC-1); every `s18` job runs `entry` first. | `GOWORK=off go -C tools/harness-gate run . entry --root ../..` on `7b5133d`: 12 blockers, exit 1 | M1 closure merges and `entry` exits 0 |
| No rehearsed binary: step 18.1 has not run ([Stage 8.2 blockers](#stage-82-blockers)), and `variant-run` runs no later S18 scenario without a binary verified against `rehearsal.sha256`. | both TOOLCHAIN-2 rows `not run` | both TOOLCHAIN-2 rows read `passed` |
| One value per input for every `s18` job. `harness-scenarios.yml` binds each `DSH_SCENARIO_*` once from environment `dsh-m2-s18` into the job env of every variant; only the `vm:*` jobs select their own (`DSH_S18_<TARGET>_*`). FS-1's `DSH_SCENARIO_FS1_DIRS` and `DSH_SCENARIO_FS1_SHARE` are paths (an ext4 directory on `linux-wsl`, an NTFS one on `windows-hosted`; `/mnt/dsh-smb/scratch` and `S:\scratch` per [runbook-smb-share.md](runbook-smb-share.md)) and `DSH_SCENARIO_FS2_CHILDREN` is a per-OS command, so one value cannot serve the Linux and the Windows jobs. | the `s18` job's `env` block and its "Select the hypervisor's inputs" step | operator answer to `s18-per-variant-inputs` |
| FS-1 needs a share on every variant. `TestFenceHoldsUnderALinkSwap` always appends `need(DSH_SCENARIO_FS1_SHARE)` to the local directories, so `windows-hosted` (GitHub-hosted, no share; variants.md gives it FS-1) fails at that input (a missing input never skips, DH-O-15), and `linux-wsl` runs the share leg that `linux-wsl:smb` owns. | `harness-gate require --stage DH-S-18 --variant windows-hosted --inputs` on `7b5133d` lists `DSH_SCENARIO_FS1_SHARE`; so do `linux-wsl` and `linux-wsl:smb` | operator answer to `s18-per-variant-inputs` |
| Refusal codes outside FS-1's allowed set on Windows. The DH-S-12 probes (1,000 calls) saw `fs/io` (2 to 5 per 1,000) and once `fs/permission-denied` while the link was swapped; FS-1 fails on any refusal other than `fs/sandbox-denied`, `fs/not-found`, `fs/not-directory` or a dependent call. Q `fs1-swap-io-codes` was never answered; accepting more codes changes the assertion, which this stage does not do on its own. | [DH-S-12.md](DH-S-12.md) Open items | operator answer to `fs1-swap-io-codes` |
| Who, and the inputs. FS-2 and VIEW-5 run on the tester's Linux machine and the Windows host, each on its own signed-in Claude Code, by the owner or an operator, never an agent session (runbook-smb-share.md "Who"; the DH-S-17 precedent). No input of the three scenarios is committed (layers, the 20 tasks, the checkouts, the descendant command, the move text). | `require --variant linux-wsl --inputs` and `--variant windows-host --inputs` on `7b5133d` | the operator or the runner's environment supplies them; none is a credential |

On an outside write the stage stops and both listings are kept (M2-R-03): FS-1 keeps its scratch
directory, both listings of O and the session log; FS-2 both listings of each task's parent directory and
the session logs; VIEW-5, since this branch, both listings of each phase's scratch directory outside the session's
working directory of that phase. The listing paths are in the test log (`kept the before and after listings of ...`).

Cleared on this stage's branch (it would have left VIEW-5's Then clause unproved on its variant; the commit
is this stage's merge):

| Defect | Seen on `7b5133d` | Fix |
| --- | --- | --- |
| VIEW-5 did not check "every model-chosen write lands inside the session's working directory" (Stage 8.6, TC-21s4) and listed nothing outside it, so a stray write elsewhere passed and M2-R-03 had no listing to keep. | `view_workdir_scenario_test.go`: only `view5-relative.txt` (in T) and `view5-original.txt` (not in R) were looked at | after each run, every path a `write`, `edit` or `apply_patch` call wrote must land inside that run's working directory (R before the move, T after it, R after the recovery), each link on the way followed, a dangling or last-component one included (`view5Written`, `view5Inside`, `view5Real`); a path whose links cannot be followed to the end (more than 40 links, a cycle, or a component or link that cannot be read) fails the scenario as unproved containment, never passes by its spelling; a failed `apply_patch` counts the mutations its part-way report lists as completed and a move destination written before its source could not be removed, not the paths it failed on or never ran (DH-ADR-FS-11). Each phase takes the hash listing of the scratch directory without only its working directory, so the inactive one of R and T is watched (after the move only T's git administration under `R/.git/worktrees` is left out), compared after each step and kept on failure (`view5Outside`). Tests: `TestView5InsideJudgesWritePaths`, `TestView5InsideFollowsLinks` (runs where symbolic links can be made: Linux, as VIEW-5), `TestView5OutsideWatchesTheInactiveDirectory`, `TestView5WrittenCountsPartialPatches` |

Probes, not evidence: none. FS-2 and VIEW-5 are signed-in runs (see "Who" above), and FS-1 has no committed
layer to probe with; `entry` forbids a recorded run either way.

## Stage 8.7 blockers

implementation-plan Stage 8.7 (impl.md 18.7 and 18.8: `DH-E-RIPGREP-1`, `DH-E-RIPGREP-2`,
`DH-E-CHOKIDAR-1`, `DH-E-CHOKIDAR-2` on the Linux (WSL) and Windows variants) was taken up on `7b5133d`
and could not run as evidence. All eight rows stay `not run` and are owed:

| Blocker | Seen | What clears it |
| --- | --- | --- |
| `harness-gate entry` exits 1 (harness:DSH-M1 not closed, M2-DEC-1), and the S18 rows run on the rehearsal's binaries, which are owed (see [Stage 8.2 blockers](#stage-82-blockers)). | `GOWORK=off go -C tools/harness-gate run . entry --root ../..` on `7b5133d`: 12 blockers, exit 1 | M1 closure merges, `entry` exits 0 and `DH-E-TOOLCHAIN-2` has passed |
| RIPGREP-1 misses its budget on Windows: the first 250 matches of sparse patterns take longer than 3 s on deepseek-harness (22,247 files, NTFS). Linux ext4 meets it on every repository and pattern (at most 0.41 s). DH-ADR-RIPGREP-1 settles a budget miss with a new decision. | Windows probe: `TODO` 19.2 s, `return nil` 9.9 s, `license` 5.4 s, `(foo\|bar\|baz)\w*\(` 5.3 s; whole searches 7-46x rg | operator decision `ripgrep1-windows-budget`: the 3 s budget stays, and a follow-up workstream speeds up `search.DirSource` on Windows; the Windows RIPGREP-1 row stays owed until that fix merges |
| Who and where. The rows run on `linux-wsl` and `windows-hosted`; CHOKIDAR-2 runs on `linux-wsl:smb` and `windows-host:smb` of [variants.md](variants.md), with `DSH_SCENARIO_REPOS`, `DSH_SCENARIO_SEARCH_BUDGET`, `DSH_SCENARIO_EDITORS`, `DSH_SCENARIO_REMOTE_DIRS` and `DSH_SCENARIO_REMOTE_CHANGE` bound by the `s18` jobs of `harness-scenarios.yml`. | `harness-scenarios.yml` on `7b5133d` | the `s18` jobs run on the merged commit once the blockers above clear |

Cleared on this stage's branch (each would have failed or misjudged the scenario):

| Defect | Seen on `7b5133d` (the last three on `2745e4c`) | Fix |
| --- | --- | --- |
| On Windows the shell helper passed the line to `cmd /C` as an argument; Go escapes `"` as `\"`, which cmd does not read, so a quoted editor path failed. | `DSH_SCENARIO_EDITORS="C:\Program Files\Git\usr\bin\vim.exe" ...`: `is not recognized`, CHOKIDAR-1 failed | the line runs from a temporary `.cmd` file (`cmd /d /c <file>`) removed in cleanup (`TestShellLineQuotesAPathWithASpace`) |
| RIPGREP-1 timed the whole unbounded search against the budget; vscode-ripgrep.md sec. 7 asks for the first 250 matches within it, with the ratio to rg recorded. | the report's `durationMs` was the whole search | the first 250 matching lines are timed against the budget; the whole search and rg's are reported as the ratio |
| RIPGREP-1 searched under the default limits (100,000 files, 1 GiB), which cut a large repository short, and compared counts only. | `search.Limits{}` in the scenario | the whole search runs with no bound and fails when cut; the file sets must equal rg's (`--no-config --no-ignore-global --follow`), each difference listed |
| RIPGREP-1 never proved that no external `rg` is spawned. | no check | a decoy `rg` first on `PATH` and a `RIPGREP_CONFIG_PATH` naming it; the scenario fails if the decoy ran |
| RIPGREP-2 failed on a tracked file that `.gitignore` matches: git lists it from the index, the harness reads ignore files, not the index. | deepseek-harness `snapshots/web/changed-files-turn/workspace.expected/app.local` (`*.local`) | the files `git ls-files --cached --ignored --exclude-standard` names are explained and logged; any other difference fails |
| The rg oracle skipped symbolic links, but the harness reads an in-root file link through `os.Root`, so on Linux the lists differed. Windows checkouts (`core.symlinks=false`) have no links. | WSL probe, deepseek-harness: `+ CLAUDE.md`, `+ packages/CLAUDE.md` (links to `AGENTS.md`) and two `tool-schemas.expected.json` links, listed by the harness only | operator decision `ripgrep1-symlinked-files`: rg runs with `--follow`; a link rg cannot follow is on its standard error and fails the comparison |
| CHOKIDAR-1 and -2 drove only edits; implementation-plan Stage 8.7 asks for create, rename, delete and burst. | the scenario bodies on `2745e4c` | after the saves (CHOKIDAR-1) and the remote change (CHOKIDAR-2, in a new directory on each remote directory), a watched directory sees a file created, written in a burst of 100 appends, renamed and deleted; each is reported exactly once within the bound (1 s; two polls) and nothing more within the quiet window (`TestWatchTransitionsCheck`, notify and poll) |
| RIPGREP-1 ignored the timing report's write and close errors, so a full disk could leave a passing run with a cut report. | `_ = enc.Encode(...)`, `_ = report.Close()` on `2745e4c` | the first write error is kept and refused writes after it; each write and the close fail the scenario (`TestTimingReportSurfacesWriteAndCloseErrors`) |
| The rg oracle split rg's list on newlines and trimmed carriage returns, misreading Linux names containing either. | `strings.Split(..., "\n")` on `2745e4c` | rg runs with `--null` and its list is split on NUL only (`TestRGFileListKeepsNewlinesAndCarriageReturns`; on WSL a tree with `line\nbreak.txt` and `cr\rname.txt` lists the same 3 files for both) |

Probes, not evidence (M2-DEC-1; a modified tree on `324f55c` with `--follow`): on WSL RIPGREP-1 passed on
all three repositories, the harness's file sets equal to rg's for every pattern, deepseek-harness's 12
in-root file links included. Earlier (a modified tree on `2745e4c`, after the three fixes above): on Windows
CHOKIDAR-1 with the transitions passed in notify mode, and RIPGREP-1 on CopilotOAuthSample passed with the
NUL-delimited oracle; on WSL CHOKIDAR-1 and CHOKIDAR-2 with the transitions passed (notify; poll on
`/mnt/e`). Earlier probes (a modified tree on `7b5133d`): on Windows (NTFS) CHOKIDAR-1 passed in
notify mode for four save strategies (vim, append, copy then append, write-temp-and-move), RIPGREP-2
passed, and RIPGREP-1 matched rg's file lists for all 30 searches with the decoy unused, missing only the
budget above. On WSL (Ubuntu-24.04, ext4 clones of CopilotOAuthSample, agent-plugin and deepseek-harness)
CHOKIDAR-1 passed in notify mode, CHOKIDAR-2 passed in poll mode on a `/mnt/e` directory changed from
Windows, RIPGREP-2 passed, and RIPGREP-1 met the budget everywhere, failing only on the symbolic links
above. CHOKIDAR-2 over SMB (`windows-host:smb`, `linux-wsl:smb`) was not probed.

## Stage 8.8 blockers

implementation-plan Stage 8.8 (impl.md 18.9 `DH-E-CONTEXT-2` and 18.10 `DH-E-STORAGE-6`) was taken up on
`7b5133d` and could not run. Nothing below is evidence of a run: the two CONTEXT-2 rows and the two
in-scope STORAGE-6 rows stay `not run` and are owed. Each open blocker on its own stops its scenario:

| Blocker | Scenario | Seen on `7b5133d` | What clears it |
| --- | --- | --- | --- |
| `harness-gate entry` exits 1: harness:DSH-M1 is not closed (M2-DEC-1), as in [Stage 8.2 blockers](#stage-82-blockers). | both | `GOWORK=off go -C tools/harness-gate run . entry --root ../..`: 12 blockers, exit 1 | M1 closure merges and `entry` exits 0 |
| No rehearsed binary: every S18 row runs the binary of the TOOLCHAIN-2 run (impl.md DH-S-18 Sequence), which has not run; `variant-run` without `--rehearsal` runs nothing. | both | [Rehearsal binaries](#rehearsal-binaries-step-181) is empty | TOOLCHAIN-2 passes on both lanes |
| No component reads instruction files. `internal/context/instructions` is imported only by its own tests and one DH-S-13 e2e test; `context.instructions` is no item of the binary's catalogue (`builtin.Sets()`, `internal/boot/builtin/builtin.go:208`, locked by `internal/boot/catalog/testdata/catalog.golden`, DH-T-BOOT-3, 35 items) and the standard preset names it as a child still to come (`internal/boot/agentpreset/presets/standard.yml:6`). [closure.md](closure.md) has DH-D-CONTEXT-2 `not built`, finishing stage DH-S-32. | CONTEXT-2 | the catalogue golden; `grep -r 'internal/context/instructions"' src/harness`: three test files | the stage that mounts DH-D-CONTEXT-2 in the release build (DH-S-32 per closure.md), or an owner decision moving that wiring into M2 (open question [`s18-context2-storage6-unbuilt`](#open-questions-stage-88)) |
| No route the layer can register. `DSH_SCENARIO_CONTEXT2_LAYER` must register `DSH_SCENARIO_CONTEXT2_ROUTE` on the scripted endpoint; the catalogue's one model item is `llm.runtime` "with no route" (`builtin.LLM`, `builtin.go:142`): no keyed or HTTP route item and no `llm.replay` item. The same gap blocks DH-S-16 checks 16.3 and 16.4 (answer to `dh-s-16-claude-route`). | CONTEXT-2 | the catalogue golden: `llm.runtime` is the only `llm.*` row | a stage puts a layer-registered route (keyed, or `llm.replay`) in the release catalogue |
| No session working directory. The scenario creates its session in `repo/sub` under `workspace-write`; `session.workdir`, `fs.local`, `fs.ops` and `sandbox.policy` are on `internal/boot/profile/bundles/optional.yml` (lines 21 to 37), waiting on `session.views`, which no item provides (DH-S-14 record). | CONTEXT-2 | `optional.yml` on `7b5133d` | the `session.views` item lands and the four move to `core.yml` |
| Nothing for the seed to run. `DSH_SCENARIO_STORAGE6_SEED` must store one attachment and one spill file through `{dsh}`; no command of `dsh --help` (help, version, catalog, completion, secret, sandbox, config, status, crash, five profiles) and no catalogue item writes either: `internal/storage/attachments` and `internal/storage/spill/...` have no item, and `core.yml` lists "attachments, spill store and policy" among the entries still to add. | STORAGE-6 | a fresh home of a `7b5133d` build after `sdk`: `blobs`, `config`, `reports`, `run`, `secrets`, `spill`, `dsh.db`, with `blobs/` and `spill/` empty | the attachment and spill stores join the release catalogue with a dsh path that stores each, or an owner decision moving that wiring into M2 (open question [`s18-context2-storage6-unbuilt`](#open-questions-stage-88)) |
| No `cache` directory. The scenario lists and reads `blobs`, `spill` and `cache` (areas/storage.md DH-E-STORAGE-6 *When*; layout `<home>/cache/request-images`) and fails when the seeded home has no `cache/`; `home.Layout.Dirs()` (`internal/storage/home/home.go:76`) has none and nothing creates it (the request cache of DH-D-STORAGE-4, P1). | STORAGE-6 | the fresh home above | the request cache creates `cache/` private, or an owner decision on the directory list (open question [`s18-storage6-cache-dir`](#open-questions-stage-88)) |
| POSIX step 2 contradicts the boot check. The scenario wants a group-writable `spill/` refused by the spill component "while the rest of the host runs"; `home.Check` (`home.go:158`) checks every layout directory at boot, so the whole home is refused `home/not-private` naming `spill/` (`TestHomeCreatedPrivate`, `private_unix_test.go`) and `DSH_SCENARIO_STORAGE6_STATUS` cannot exit 0. | STORAGE-6 (Linux) | `checkPrivate` (`private_unix.go`): any group or other bit on a layout directory is a problem | owner decision (open question [`s18-storage6-spill-refusal`](#open-questions-stage-88)) |
| Who and where. The second account is created and kept by the tester (WSL) and the Windows host keeper, never by an agent session ([runbook-two-account.md](runbook-two-account.md)); CONTEXT-2 runs on `linux-wsl` and `windows-hosted`. | both | `require --stage DH-S-18 --variant` on `7b5133d`: `linux-wsl` and `windows-hosted` lists end `DH-E-CONTEXT-2`; `linux-wsl:two-account` and `windows-host:two-account` print `DH-E-STORAGE-6`; each exit 0 | the `s18` jobs of `harness-scenarios.yml` (or the lanes' `--acceptance DH-S-18`) run on the merged commit once the blockers above clear |

Checked and consistent (no change): the CONTEXT-2 scenario's oracles match the package it will exercise,
`frameOpen = "<instructions>\n"` (`internal/context/instructions/render.go:51`) and the record's
`refused[].path` (`render.go:26`, `:40`); STORAGE-6's input contract matches
`tools/harness-gate/variants/inputs_test.go` (the host commands on POSIX only).

When they run, each CONTEXT-2 row takes the run's commit, the environment (`E2 wsl:<host>/<distro>` or
`E2 runner:<name>`), the versions of `git` and the editor, `passed` only if
`TestContextInstructionFileChangesReachModel` passed, and in Artifacts the SHA-256 of the binary it
executed (one of the rehearsal binaries) and the run id; each STORAGE-6 row the same for
`TestAnotherLocalUserCannotReadStoredData`, plus the filled record of the two-account runbook.

### Open questions (Stage 8.8)

Raised by the blockers above and owed an owner answer; tech-spec.md
[sec. 10](../tech-spec.md#10-open-questions) holds the story-wide M2-Q-1 to M2-Q-7. Each default stands
until answered, and none lets a row read anything but `not run`:

| ID | Question | Default until answered | Blocks |
| --- | --- | --- | --- |
| `s18-context2-storage6-unbuilt` | CONTEXT-2 and STORAGE-6 exercise components the release build does not mount (DH-D-CONTEXT-2 `not built`, finishing DH-S-32 per [closure.md](closure.md); the attachment and spill stores have no catalogue item or dsh path). Does M2 take on that wiring, or do the two scenarios wait for the stages that build it? | They wait: no wiring moves into M2; the CONTEXT-2 and STORAGE-6 rows stay `not run` | DH-E-CONTEXT-2 and DH-E-STORAGE-6 rows; S18 closure (M2-Q-3 pattern: S18 stays *open*) |
| `s18-storage6-cache-dir` | STORAGE-6 lists and reads `blobs`, `spill` and `cache`, but `home.Layout.Dirs()` has no `cache/` and only the P1 request cache (DH-D-STORAGE-4) would create it. Does the scenario drop `cache` until that cache lands, or must the layout create `cache/` private now? | The scenario text stands (areas/storage.md unchanged, DC-2): `cache/` is required, so the row waits for the request cache | DH-E-STORAGE-6 rows; S18 closure |
| `s18-storage6-spill-refusal` | POSIX step 2 wants a group-writable `spill/` refused by the spill component while the rest of the host runs; `home.Check` refuses the whole home `home/not-private` at boot. Which behaviour is correct: a per-component refusal (change `home.Check`), or the boot refusal (change the scenario's expectation)? | The boot refusal stands (no code change); the Linux STORAGE-6 row cannot pass and stays `not run` | DH-E-STORAGE-6 (Linux) row; S18 closure |

## Stage 8.9 blockers

implementation-plan Stage 8.9 (impl.md 18.11: `DH-E-NODEFS-1`, 100 hard power cuts per guest OS, run
last) was taken up on `a4e9837` and could not run as evidence. Both rows stay `not run` and are owed:

| Blocker | Seen | What clears it |
| --- | --- | --- |
| `harness-gate entry` exits 1 (harness:DSH-M1 not closed, M2-DEC-1), and the S18 rows run on the rehearsal's binaries, which are owed (see [Stage 8.2 blockers](#stage-82-blockers)). | `GOWORK=off go -C tools/harness-gate run . entry --root ../..` on `a4e9837`: 12 blockers, exit 1 | M1 closure merges, `entry` exits 0 and `DH-E-TOOLCHAIN-2` has passed |
| Who and where. The rows run on the team-kept KVM/libvirt and Hyper-V hosts from the `[self-hosted, linux, dsh-hypervisor]` runner (Q `m2-e2-fleet`), never in an agent session; none is reachable from this stage's session. | [runbook-power-cut.md](runbook-power-cut.md) | the hosts are provisioned, the `power-cut` preflight passes, and the `vm:*` s18 jobs (or the runbook's step 5) run on the merged commit |
| Sharding past 3 h (M2-R-06) is a manual run: the `vm:*` jobs of `harness-scenarios.yml` run all 100 rounds and do not bind the optional `DSH_SCENARIO_NODEFS_ROUNDS`. | `harness-scenarios.yml` on `a4e9837` | a run within 3 h, or the runbook's manual shards, or a workflow change binding the input |

Cleared on this stage's branch (each would have failed or misjudged the scenario):

| Defect | Seen on `a4e9837` (the last three on `5b6146c`; the bound's last on `426c1c5`) | Fix |
| --- | --- | --- |
| `variant-run` ran `DH-E-NODEFS-1` in file order, not last (impl.md 18.11, architecture.md rule 7). | `stageOrder["DH-S-18"]` had no `Last` | NODEFS-1 is the stage's last phase (`TestPhasesOrderDHS18`) |
| The cut came 1-5 s after the writer was started and the round failed if nothing was acknowledged yet, so a slow guest boot failed the scenario. | fixed delay after START | each round waits (at most 5 min) for the writer's first new acknowledgement, then cuts at a random moment within 5 s (`TestNodefsDriveAgainstAStandInGuest`, `TestNodefsDriveNeedsTheWriterRunningBeforeTheCut`) |
| The acknowledged bound was taken right after the power-off, so an acknowledgement in flight at the cut was not judged against; taken right after the read instead, one the host had received but the recorder not yet read could still be missed, or counted in the next round; reading the cut's connections for 2 s more after the read took the deadline's expiry for completion, so an acknowledgement delayed past it was dropped. | bound sampled at the cut (`a4e9837`), right after the read (`5b6146c`), after a 2 s settle (`426c1c5`) | no timer decides it. A version is acknowledged once the recorder has logged it and replied `ok <version>`, and the writer writes no next version before that reply (`TestNodefsWriterWaitsForTheRecordersConfirmation`). The judgement and the bound are taken under one lock that ties each open writer connection to the version judged; an acknowledgement read on it afterwards, however late, is logged `late` if no newer, and otherwise logged `fail` and fails the run (the next round's check, the drive's end, or `close`) (`TestNodefsJudgesAcksReadAfterTheirRoundWasJudged`: delivered during the next round and after the last). |
| A shard resumed after one stopped between a round's cut and its read, so the next writer replaced the unjudged file. | `continueLog` on `5b6146c` read only `read` and `fail` lines | a `round` or `cut` line after the last `read` refuses the shard and names the round never judged (`TestNodefsShardContinuesTheLog`) |
| A late acknowledgement's `fail` line can precede a later round's `read`, and `continueLog` judged only each guest's last event, so a shard resumed the failed run. | `continueLog` on `ab5eab2` (`ended[vm]` overwritten per event) | any `fail` line of the run refuses every later shard until a fresh `start rounds=1-...` line (`TestNodefsJudgesAcksReadAfterTheirRoundWasJudged` reopens its failed log; `TestNodefsShardContinuesTheLog`: late fail refused, fresh run accepted) |
| The runbook named `DSH_SCENARIO_REPORT` as the version log; under `scenario.Main` the test writes it to `<report>.detail`. | runbook-power-cut.md on `5b6146c` | the runbook keeps and records both `<report>` and `<report>.detail` |
| The recorder kept its log in memory and wrote it only on a failure; architecture.md asks for an append-only log, kept. | log written at the end | every event is appended with a sequence number and the round, synced and kept whatever the result (`TestNodefsDriveCatchesALostAcknowledgedVersion`) |
| No sharding (M2-R-06). | 100 rounds per run | optional `DSH_SCENARIO_NODEFS_ROUNDS=i-j`; a shard continues the previous shard's log and refuses unless it ends with every guest's round i-1 read (`TestNodefsShardContinuesTheLog`, `TestNodefsRoundsOf`) |
| READ's standard error was judged with the file; a CRLF header was refused; trailing bytes after the payload were not checked. | combined output, `\n` only | standard output only; the version must match byte for byte, one trailing line ending allowed (`TestNodefsJudge`) |
| The writer replaced the file with `fsx.WriteFile`, not `fsx.Replace` as DH-E-NODEFS-1 names. | writer loop | `fsx.Replace` with `Durable: true`; a version is acknowledged only once it returned nil |

## Stage 8.10 closure attempt

implementation-plan Stage 8.10 (the two-step close of Stage 1.1) was taken up on `1eb8f67`. The record
cannot close: the status line stays `Closure status: open` and no closing PR is opened.

| Check | Seen on `1eb8f67` | What clears it |
| --- | --- | --- |
| `harness-gate ready --stage DH-S-18` | `GOWORK=off go -C tools/harness-gate run . ready --stage DH-S-18 --root ../..`: `NOT READY, 46 blockers`, exit 1; every blocker an in-scope row reading `not run` (the 38 owed rows less their 8 macOS rows, and the 16 machine rows added below), each named by scenario and variant | every in-scope row reads `passed` on one merged commit (the stage blockers above) |
| `harness-gate entry` | 12 blockers, exit 1 (harness:DSH-M1 not closed, M2-DEC-1) | M1 closure merges |
| `harness-gate records` and `harness-gate variants` | both exit 0: the record keeps its shape, and every scenario has an in-scope variant | (held) |

Done on this stage's branch:

- The Stage 8.3, 8.4, 8.5, 8.6 and 8.8 sections above, the step notes 18.2 to 18.6, 18.9 and 18.10, and
  the reason of the VIEW-5 row were lost when the Stage 8.7 record merged (`4de9765` replaced them with
  its own section); they are restored as merged on `7cc8b99`, unchanged.
- The Scenarios table has one row per in-scope variant of [variants.md](variants.md) (M2-DOD-4): the
  `linux-e2:*` machines of EXEC-1, EXEC-10, NATIVESYSTEM-1 to -4 and SANDBOX-1, `linux-wsl:smb` and
  `windows-host:smb` for FS-1, and `windows-host:smb` for FS-2 had none; each reads `not run` with its
  reason and keeps S18 open (M2-C-2). The macOS rows of TOOLCHAIN-2 and CONTEXT-2 and the `vm:macos-guest`
  row of NODEFS-1 are added out of scope by decision `platforms-windows-wsl` (variants.md Scope).
- `TestNoPartialClosureNamesTheVariantNotRun` (`tools/harness-gate/closure`): a record with
  `linux-e2:systemd` passed and `linux-e2:no-user-manager` not run is refused naming
  `DH-E-EXEC-10 [linux-e2:no-user-manager]`, and its `closed` line `closed-but-not-ready`.
- `TestStageClosureS18Golden` and `TestStageClosureS18ExitCodes` (`tools/harness-gate`, golden
  `testdata/closure/expected_s18.golden`): the command-level `ready` then `close --stage DH-S-18` over a
  fixture of this record with all 19 scenarios passed on every in-scope variant, against the committed
  acceptance index and variants.md. Open and ready, ready exits 0 and close is refused `not-yet-closed`;
  closed and ready, both exit 0; with the `linux-e2:no-user-manager` row of EXEC-10 not run, both are
  refused naming it. `TestS18EveryVariantHasARow` checks that the fixture passes, and this record has a row
  of, every in-scope variant and scenario of variants.md. `TestRecordLiveS18` runs the protocol on this
  record: while it reads open, close is refused `not-yet-closed`.

When every in-scope row has passed: `ready --stage DH-S-18` exits 0 on the record reading `open`; the
closing PR changes only the status line to `Closure status: closed`, and its gate runs
`harness-gate close --stage DH-S-18`; [closure.md](closure.md) moves the rows of
[Phase 8 (DH-S-18)](closure.md#phase-8-dh-s-18) in the same PR.

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

Owner answers owed: the three [Stage 8.8 open questions](#open-questions-stage-88).
