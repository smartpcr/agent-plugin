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
| 18.3 | DH-E-EXEC-1, DH-E-EXEC-10 | | |
| 18.4 | DH-E-NATIVESYSTEM-1 to -4, DH-E-SANDBOX-1 | | |
| 18.5 | DH-E-FS-1, DH-E-FS-2 | | |
| 18.6 | DH-E-VIEW-5 | | |
| 18.7 | DH-E-RIPGREP-1, DH-E-RIPGREP-2 | | |
| 18.8 | DH-E-CHOKIDAR-1, DH-E-CHOKIDAR-2 | | |
| 18.9 | DH-E-CONTEXT-2 | | not run; blocked, see [Stage 8.8 blockers](#stage-88-blockers) |
| 18.10 | DH-E-STORAGE-6 | | not run; blocked, see [Stage 8.8 blockers](#stage-88-blockers) |
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
| DH-E-EXEC-1 | Linux (E2) | acceptance | required | E2 | | not run (stage not started) | |
| DH-E-EXEC-10 | Linux (E2, over SSH) | acceptance | required | E2 | | not run (stage not started) | |
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
| DH-E-CONTEXT-2 | Linux (WSL) | acceptance | required | E2 | | not run (blocked: harness-gate entry fails, M2-DEC-1; the release build has no instruction-file component and no route, see Stage 8.8 blockers) | |
| DH-E-CONTEXT-2 | Windows | acceptance | required | E2 | | not run (blocked: harness-gate entry fails, M2-DEC-1; the release build has no instruction-file component and no route, see Stage 8.8 blockers) | |
| DH-E-STORAGE-6 | Linux (WSL) | acceptance | required | E2 | | not run (blocked: harness-gate entry fails, M2-DEC-1; dsh cannot seed an attachment or a spill file, see Stage 8.8 blockers) | |
| DH-E-STORAGE-6 | Windows | acceptance | required | E2 | | not run (blocked: harness-gate entry fails, M2-DEC-1; dsh cannot seed an attachment or a spill file, see Stage 8.8 blockers) | |
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

## Stage 8.8 blockers

implementation-plan Stage 8.8 (impl.md 18.9 `DH-E-CONTEXT-2` and 18.10 `DH-E-STORAGE-6`) was taken up on
`7b5133d` and could not run. Nothing below is evidence of a run: the two CONTEXT-2 rows and the two
in-scope STORAGE-6 rows stay `not run` and are owed. Each open blocker on its own stops its scenario:

| Blocker | Scenario | Seen on `7b5133d` | What clears it |
| --- | --- | --- | --- |
| `harness-gate entry` exits 1: harness:DSH-M1 is not closed (M2-DEC-1), as in [Stage 8.2 blockers](#stage-82-blockers). | both | `GOWORK=off go -C tools/harness-gate run . entry --root ../..`: 12 blockers, exit 1 | M1 closure merges and `entry` exits 0 |
| No rehearsed binary: every S18 row runs the binary of the TOOLCHAIN-2 run (impl.md DH-S-18 Sequence), which has not run; `variant-run` without `--rehearsal` runs nothing. | both | [Rehearsal binaries](#rehearsal-binaries-step-181) is empty | TOOLCHAIN-2 passes on both lanes |
| No component reads instruction files. `internal/context/instructions` is imported only by its own tests and one DH-S-13 e2e test; `context.instructions` is no item of the binary's catalogue (`builtin.Sets()`, `internal/boot/builtin/builtin.go:208`, locked by `internal/boot/catalog/testdata/catalog.golden`, DH-T-BOOT-3, 35 items) and the standard preset names it as a child still to come (`internal/boot/agentpreset/presets/standard.yml:6`). [closure.md](closure.md) has DH-D-CONTEXT-2 `not built`, finishing stage DH-S-32. | CONTEXT-2 | the catalogue golden; `grep -r 'internal/context/instructions"' src/harness`: three test files | the stage that mounts DH-D-CONTEXT-2 in the release build (DH-S-32 per closure.md), or an owner decision moving that wiring into M2 (open question `s18-context2-storage6-unbuilt`) |
| No route the layer can register. `DSH_SCENARIO_CONTEXT2_LAYER` must register `DSH_SCENARIO_CONTEXT2_ROUTE` on the scripted endpoint; the catalogue's one model item is `llm.runtime` "with no route" (`builtin.LLM`, `builtin.go:142`): no keyed or HTTP route item and no `llm.replay` item. The same gap blocks DH-S-16 checks 16.3 and 16.4 (answer to `dh-s-16-claude-route`). | CONTEXT-2 | the catalogue golden: `llm.runtime` is the only `llm.*` row | a stage puts a layer-registered route (keyed, or `llm.replay`) in the release catalogue |
| No session working directory. The scenario creates its session in `repo/sub` under `workspace-write`; `session.workdir`, `fs.local`, `fs.ops` and `sandbox.policy` are on `internal/boot/profile/bundles/optional.yml` (lines 21 to 37), waiting on `session.views`, which no item provides (DH-S-14 record). | CONTEXT-2 | `optional.yml` on `7b5133d` | the `session.views` item lands and the four move to `core.yml` |
| Nothing for the seed to run. `DSH_SCENARIO_STORAGE6_SEED` must store one attachment and one spill file through `{dsh}`; no command of `dsh --help` (help, version, catalog, completion, secret, sandbox, config, status, crash, five profiles) and no catalogue item writes either: `internal/storage/attachments` and `internal/storage/spill/...` have no item, and `core.yml` lists "attachments, spill store and policy" among the entries still to add. | STORAGE-6 | a fresh home of a `7b5133d` build after `sdk`: `blobs`, `config`, `reports`, `run`, `secrets`, `spill`, `dsh.db`, with `blobs/` and `spill/` empty | the attachment and spill stores join the release catalogue with a dsh path that stores each |
| No `cache` directory. The scenario lists and reads `blobs`, `spill` and `cache` (areas/storage.md DH-E-STORAGE-6 *When*; layout `<home>/cache/request-images`) and fails when the seeded home has no `cache/`; `home.Layout.Dirs()` (`internal/storage/home/home.go:76`) has none and nothing creates it (the request cache of DH-D-STORAGE-4, P1). | STORAGE-6 | the fresh home above | the request cache creates `cache/` private, or an owner decision on the directory list (open question `s18-storage6-cache-dir`) |
| POSIX step 2 contradicts the boot check. The scenario wants a group-writable `spill/` refused by the spill component "while the rest of the host runs"; `home.Check` (`home.go:158`) checks every layout directory at boot, so the whole home is refused `home/not-private` naming `spill/` (`TestHomeCreatedPrivate`, `private_unix_test.go`) and `DSH_SCENARIO_STORAGE6_STATUS` cannot exit 0. | STORAGE-6 (Linux) | `checkPrivate` (`private_unix.go`): any group or other bit on a layout directory is a problem | owner decision (open question `s18-storage6-spill-refusal`) |
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
