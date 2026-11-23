# DH-S-6 evidence

Decisions governing this record ([decisions.md](../decisions.md)): `platforms-windows-wsl` (the macOS rows below are historical), M1-D-CLOSE-1 (ticks made while the gate was red are provisional until the closure commit), and the open M1-D-BOOT-1, M1-D-BOOT-2 and M1-D-BOOT-3 (cut-short deliverables, DH-T-HOST-17, the usage-error status). Logged by gap-closure-plan Stage 12.1; merge commit: recorded when the Stage 12.1 change merges.

Closure status: open

This line becomes `closed at <sha>` only in plan step 18.1.14, at the M1 closure commit.
The "Closure status: closed" paragraph below is the Stage 6.7 closure, kept as history:
under M1-D-CLOSE-1 every tick made while the gate was red is provisional and is
re-verified at that commit, and plan Stage 14.6 re-records the manual checks on the two
M1 lanes. The fields of impl.md sec. 7.3 and the per-step manual-check register are at
the end of this file, in "Evidence record (impl.md sec. 7.3)"; how to fill them is in
[the evidence runbook](README.md).

Stages 6.1 to 6.6 (impl.md steps 6.1 to 6.16) were built by the earlier workstreams
of this phase, merged to `main` up to `f394c24`. Stage 6.7 (step 6.17 and the circle
closure) carries the closure record for DH-S-6. Every result below was re-run on one
tree: `f394c24` plus this stage's change, the commit that carries this record. All
seventeen deliverables are built at that commit.

**Closure status: closed.** Every closure item is recorded below. The manual checks
of impl.md DH-S-6, including "a child that ignores termination ... on each operating
system", are recorded on Windows, Linux and macOS (macOS: historical, `platforms-windows-wsl`): Windows 11 on this runner, and all
three systems in `harness-manual-checks` run
[44860891149](https://github.com/smartpcr/agent-plugin/actions/runs/44860891149)
(success; jobs 136713767008 Linux, 136713766919 macOS (historical), 136713767118 Windows) at
commit `42d0d7dbd163a46e3e395619d3c9f53c5aafff1a`, go1.26.0. The commit that carries
this record differs from `42d0d7d` only in this file, the plan's tick and the
workflow's push paths (which now include this file, so the closing commit's push runs
the same checks on all three systems again). No code or test changed, so the results
hold at the closing commit.

> **Historical (decision `platforms-windows-wsl`, 2026-11-22).** Every macOS result in
> this record (the macOS jobs of runs 44860891149 and 44861208866, the macOS column of
> the manual-check table and the macOS owner line) was recorded before macOS left M1
> scope. It is kept as history and counts as neither a pass nor a skip for M1; the M1
> lanes are Windows (native) and Linux (WSL).

## Deliverables built at the closing commit

| Step | Deliverable | Where (`src/harness/internal/`) | Stage |
| --- | --- | --- | --- |
| 6.1 | `DH-D-UTIL-5` leaf packages | `util/herr`, `util/ident`, `util/homedir`, `util/mediatype` | 6.1 |
| 6.2 | `DH-D-EXEC-1` one spawn package | `world/subprocess` (`Start`, `BuildEnv`, `LookPath`) | 6.1 |
| 6.3 | `DH-D-EXEC-2` tree stop and await | `world/subprocess` (`StopTree`, `Wait`; job object, cgroup, process group) | 6.1 |
| 6.4 | `DH-D-BOOT-2` launch facts | `boot/launch` | 6.2 |
| 6.5 | `DH-D-STORAGE-2` private storage directories | `storage/home` (`Ensure`, `CheckPrivateFile`) | 6.2 |
| 6.6 | `DH-D-HUMAN-7` secret references | `human/secret` | 6.2 |
| 6.7 | `DH-D-BOOT-3` profiles, bundles, layer stack | `boot/profile` | 6.3 |
| 6.8 | `DH-D-CLI-1` one command tree | `cli`, `cli/cmdline` | 6.3 |
| 6.9 | `DH-D-CLI-3` output contract | `cli/output` | 6.3 |
| 6.10 | `DH-D-HOST-7` fatal path and report | `host/diag` | 6.4 |
| 6.11 | `DH-D-BOOT-5` boot sequence and shutdown | `boot`, `boot/logging` | 6.4 |
| 6.12 | `DH-D-BOOT-6` preview, explain, check | `boot/config` (H-21 open) | 6.5 |
| 6.13 | `DH-D-BOOT-7` validated reload and one edit path | `boot/config` | 6.5 |
| 6.14 | `DH-D-STORAGE-8` live settings edits | `storage/settings` | 6.5 |
| 6.15 | `DH-D-HOST-4` nothing leaves by default | `host/sharing` | 6.6 |
| 6.16 | `DH-D-UTIL-2` one egress policy | `util/egress` | 6.6 |
| 6.17 | `DH-D-UTIL-6` the test kit | `testkit/...` | 6.7 |

## Stage 6.7 -- Harness Test Kit and Circle Closure (closes DH-S-6)

### What changed

The kit (`internal/testkit`, README there). Every helper takes the test and
registers its release with `t.Cleanup` (QG-5):

- `testkit`: `Tree(t)`, a cordis root with no exporters, disposed at cleanup;
  `Buffer`, a goroutine-safe writer.
- `clock`: the virtual clock (`Manual`), now with its TC-06a test.
- `temphome`: `Dir`, `New` (a laid-out `home.Layout`), `Write`, `Layer`,
  `UserLayer`; the home is removed at cleanup and a surviving home fails the test.
- `model`: the fake model. `Transport` is an `http.RoundTripper` serving scripted
  SSE turns (`Reply`, `Status`, `Text`, `Usage`, `Finish`, `Error`, `Hang`);
  `Adapter` is an `llm.Adapter` over any round tripper; `Register` adds the route to
  a real `llm.Runtime`. Unused turns and unscripted requests fail the test.
- `answerer`: the fake answerer, registered on a real `human.Registry`, answering
  with `AllowOnce`, `AllowSession`, `Deny`, `Text` or `Never`.
- `permit`: an egress permit whose context ends at cleanup or `Withdraw`.
- `launcher`: `Build` (a `go build` through `world/subprocess`) and `Start`, which
  starts a program through `world/subprocess` on a fresh temp home and returns a
  `src/sdk/jsonrpc` client on its stdio. Cleanup closes the client and stdin, stops
  and awaits the whole tree (survivors fail the test), then removes the home.
- `archtest`: only tests and the kit's own packages import `internal/testkit`
  (DH-T-UTIL-11).
- `src/harness/go.mod`: `src/sdk` pinned to `f394c24`, the first pin with
  `jsonrpc.Conn`, so the `GOWORK=off` release build compiles the launcher
  (DH-ADR-26).

The hand-written fakes of steps 6.1 to 6.16 are replaced by the kit:

| Test file | Replaced | By |
| --- | --- | --- |
| `boot/boot_test.go`, `boot/audit_test.go` | `syncBuffer`; the hand-made temp home and overlay writes | `testkit.Buffer`; `temphome.Dir`, `temphome.Write` |
| `boot/logging/logging_test.go` | `syncBuffer`; cordis roots never disposed | `testkit.Buffer`; `testkit.Tree` |
| `boot/config/service_test.go` | `newHome`'s temp dir, `write`, the boot root | `temphome.Dir`, `temphome.Write`, `testkit.Tree` |
| `storage/settings/settings_test.go` | temp home, `write`, root plus cleanup pair | `temphome.Dir`, `temphome.Write`, `testkit.Tree` |
| `util/egress/egress_test.go`, `resolve_test.go` | `fakePermit` | `permit.New`, `Withdraw` |

Kept on purpose (DH-ADR-UTIL-6: a seam that belongs to one package stays beside
its contract): boot's `fakeSteps`/`hungSteps` (the `Steps` seam of `boot`), the
secret store's `fakeSource` (an internal package), and the `httptest` servers of
`host/sharing` and `util/egress` (`countingServer`), which are the remote peer, not a
fake of a harness component.

### Tests

| Scenario (implementation-plan.md Stage 6.7) | Test |
| --- | --- |
| Virtual clock drives deadlines without sleeping (TC-06a) | `TestVirtualClockDrivesDeadlinesWithoutSleeping` (`testkit/clock`): a component of a kit tree waits 30 s; it has not fired at 30 s - 1 ns and fires at 30 s, in under 100 ms of wall time. |
| Fake model scripts a turn (TC-06a) | `TestFakeModelScriptsATurn` (`testkit/model`): the real `llm.Runtime` with a memstore session makes one attempt through the fake transport and the caller settles one `assistant/message` with the scripted text. `TestFakeModelScriptsFailures` covers a status reply, an in-stream error and a hang ended by cancellation. |
| Temp home torn down by cleanup (TC-06b/TC-06c) | `TestTempHomeTornDownByCleanup` (`testkit/temphome`) and `TestLaunchedProgramAndHomeTornDownByCleanup` (`testkit/launcher`): a program built from `testdata/echo` is started on a temp home, answers `home`, `echo` and `spawn` (a grandchild that inherits nothing) over JSON-RPC; after the subtest the home is gone and neither pid is alive. |
| DH-S-6 scenarios authored and indexed | `TestAuthoredScenariosAreIndexedAndCompileButDoNotRun` and `TestRequireSubcommand` (`tools/harness-gate`), below. |

```text
$ go -C src/harness test -count=1 ./internal/testkit/...
ok   .../internal/testkit/answerer
ok   .../internal/testkit/clock
ok   .../internal/testkit/golden
ok   .../internal/testkit/launcher
ok   .../internal/testkit/model
ok   .../internal/testkit/temphome
```

`go -C src/harness test ./...`, `go -C src/harness vet ./...`, `go vet` with
`GOOS=linux CGO_ENABLED=0` and with `GOOS=darwin`, and `GOWORK=off go build ./...`
all pass; `gofmt -l src/harness` is clean. (The `GOOS=darwin` vet is a cross-build check
run on Windows, not a macOS lane; macOS is out of scope, `platforms-windows-wsl`.)

## DH-S-6 exit checks

### Tests that waited on a later step of circle C-1, passing at this commit

Each was written against the interface of a later step and passes only with it:

```text
$ go -C src/harness test -count=1 -v ./internal/boot/ ./internal/boot/logging/ \
    ./internal/boot/config/ ./internal/storage/settings/ ./internal/util/egress/ \
    ./internal/host/sharing/ ./internal/archtest/
--- PASS: TestAuditOffSwitchListsEveryKindOff            (6.11 waits on 6.15)
--- PASS: TestRealAuditReadsOffSwitchFromLaunchEnv       (6.11 waits on 6.4, 6.15)
--- PASS: TestComposedSharingSettingsReachTheAudit       (6.11 waits on 6.7, 6.15)
--- PASS: TestUnreadableCertificateStopsTheLaunch        (6.11 waits on 6.16)
--- PASS: TestBrokenRequiredEntryFailsBootOnce           (6.11 waits on 6.10)
--- PASS: TestFatalShutdownOverrunIsBounded              (6.11 waits on 6.10)
--- PASS: TestReloadAfterReadyAppliesValidAndRejectsInvalidLayers (6.11 waits on 6.13)
--- PASS: TestStuckReloadDrainSharesTheShutdownBound     (6.11 waits on 6.13)
--- PASS: TestBadLayerEditLeavesRunningTreeUnchanged     (6.13 waits on 6.7)
--- PASS: TestRequiredEntriesSurviveReloadAndEdit        (6.13 waits on 6.11)
--- PASS: TestRefusedEditsChangeNothing                  (6.14 waits on 6.13)
--- PASS: TestDescribeShowsEffectiveValueAfterRejection  (6.14 waits on 6.13)
--- PASS: TestWithdrawnPermitCancelsAndRefuses           (6.16 waits on 6.15, 6.17)
--- PASS: TestNoPermitNoDial                             (6.16 waits on 6.17)
--- PASS: TestSwitchWithdrawsLivePermit                  (6.15 waits on 6.13)
--- PASS: TestImportDirection                            (6.17 rule)
--- PASS: TestNoSpawnOutsideSubprocess                   (6.2)
ok   .../internal/boot  ok .../boot/logging  ok .../boot/config  ok .../storage/settings
ok   .../util/egress    ok .../host/sharing  ok .../archtest
```

Every other test of these packages passes in the same run (12 in `boot`, 6 in
`boot/logging`, 28 in `boot/config`, 9 in `storage/settings`, 8 in `util/egress`,
6 in `host/sharing`, 6 in `archtest`).

### Manual checks (impl.md DH-S-6; `manual`)

Recorded on all three systems; the macOS results are historical (decision
`platforms-windows-wsl`). They are manual checks of this stage, not the DH-E
scenario runs that DH-S-18 owns. Windows 11 was recorded on this runner. Linux and
macOS (historical) (and Windows again) were recorded by `.github/workflows/harness-manual-checks.yml`
(keyless, read-only, no scenario), because no Linux or macOS (historical) host was available
locally and the CI `harness` gate lanes never reach these tests: the gate stops at
gate 2 (analyzer vet) on findings that predate this stage (for example
`boot/audit_test.go:70,91`, `boot/boot_test.go:98,434,632`). The workflow runs on
`ubuntu-latest`, `macos-latest` (historical) and `windows-latest` on every push to a
`ws/harness-**` branch that touches the tree owner, the test kit, boot, `host/diag`
or this record, and by hand (`workflow_dispatch`). It runs the tests directly rather than
through the gate, writes the owner line and stop time to its job summary, and uploads
the output as `manual-checks-<os>`.

Run [44860891149](https://github.com/smartpcr/agent-plugin/actions/runs/44860891149),
`push` at `42d0d7dbd163a46e3e395619d3c9f53c5aafff1a`, conclusion `success`. Excerpts from
the uploaded `tree-stop-<OS>.txt` (the Darwin excerpt is historical, `platforms-windows-wsl`):

```text
# Linux runnervm0hi8g 7.0.0-1020-azure x86_64, go1.26.0 linux/amd64 (job 136713767008)
subprocess_test.go:88: owner process-group (fallback: own control group not writable; delegated scope not requested (U-K1-1)); stopped in 2.011083403s
--- PASS: TestStopTreeStopsAndAwaitsTheTree (2.02s)
--- PASS: TestContextEndStopsTheTree (0.00s)
--- PASS: TestContextEndStopsDescendantsAfterChildExit (0.01s)
--- PASS: TestTreeOwnerIsReported (0.00s)
--- PASS: TestRunTimeoutStopsTheTreeHoldingItsOutput (2.39s)
--- PASS: TestLaunchedProgramAndHomeTornDownByCleanup (0.18s)

# Darwin 25.6.0 arm64, go1.26.0 darwin/arm64 (job 136713766919)
subprocess_test.go:88: owner process-group; stopped in 2.016072125s
--- PASS: TestStopTreeStopsAndAwaitsTheTree (2.02s)
--- PASS: TestContextEndStopsTheTree (0.00s)
--- PASS: TestContextEndStopsDescendantsAfterChildExit (0.02s)
--- PASS: TestTreeOwnerIsReported (0.00s)
--- PASS: TestRunTimeoutStopsTheTreeHoldingItsOutput (2.57s)
--- PASS: TestLaunchedProgramAndHomeTornDownByCleanup (0.33s)

# Windows, go1.26.0 windows/amd64 (job 136713767118)
subprocess_test.go:88: owner job-object; stopped in 2.0018494s
--- PASS: TestStopTreeStopsAndAwaitsTheTree (2.04s)
```

Confirming run at the commit that first carried this record:
[44861208866](https://github.com/smartpcr/agent-plugin/actions/runs/44861208866),
`push` at `2302fc822fad10cd63c727442277f5e29ea110e6`, conclusion `success` (jobs
136714770420 Linux, 136714770672 macOS (historical), 136714770648 Windows). Linux: `owner
process-group (fallback: own control group not writable; delegated scope not
requested (U-K1-1)); stopped in 2.010563566s`; macOS (historical): `owner process-group; stopped
in 2.014288583s`; Windows: `owner job-object; stopped in 2.0018977s`.

The broken-entry and bad-layer tests (`boot-<OS>.txt`) pass on all three lanes (the macOS lane historical, `platforms-windows-wsl`):
`TestBrokenRequiredEntryFailsBootOnce`, `TestReloadAfterReadyAppliesValidAndRejectsInvalidLayers`,
`TestBadLayerEditLeavesRunningTreeUnchanged`, `TestFatalPrintsOneMessageAndWritesOneReport`.

The bound: `StopTree` is called with a 2 s grace and a 15 s context. The grandchild
ignores the polite stop (`subprocesstest.RoleStubborn`: SIGTERM, SIGINT and SIGHUP
ignored on POSIX), so the tree outlives the grace and is force-stopped. Process
group SIGKILL on Linux and macOS (historical), the Job Object on Windows. On every system the tree
is gone about 2.0 s after the stop began: inside the bound, and only the grace long.

To repeat a row by hand, run `go -C src/harness test -count=1 -v -run
'TestStopTreeStopsAndAwaitsTheTree|TestContextEndStopsDescendantsAfterChildExit|TestTreeOwnerIsReported'
./internal/world/subprocess/` on the system.

| Check | Windows 11 | Linux | macOS (historical, `platforms-windows-wsl`) |
| --- | --- | --- | --- |
| Boot a tree with a broken required entry: one fatal message, one report file | Recorded. `TestBrokenRequiredEntryFailsBootOnce` (one message naming the entry, exit 78, one report under `reports/`), `TestFatalPrintsOneMessageAndWritesOneReport`. | Recorded, run 44860891149 (job 136713767008) at `42d0d7d`: both pass. | Recorded, run 44860891149 (job 136713766919) at `42d0d7d`: both pass. |
| A child that ignores termination: the tree is gone within the bound | Recorded. `TestStopTreeStopsAndAwaitsTheTree`: `owner job-object; stopped in 2.007s` (grace 2 s, then the job is terminated); `TestContextEndStopsDescendantsAfterChildExit`; the kit's launcher test leaves no pid alive. CI run 44860891149: `owner job-object; stopped in 2.0018494s`. | Recorded, run 44860891149 (job 136713767008) at `42d0d7d`: `owner process-group (fallback: own control group not writable; delegated scope not requested (U-K1-1)); stopped in 2.011083403s`; context-end, orphan and launcher timeout tests pass, no pid alive. | Recorded, run 44860891149 (job 136713766919) at `42d0d7d`: `owner process-group; stopped in 2.016072125s`; context-end, orphan and launcher timeout tests pass, no pid alive. |
| Edit a layer file badly: the running tree is unchanged | Recorded. `TestBadLayerEditLeavesRunningTreeUnchanged` (the rejection names file, line and column; the tree's config and start count are unchanged); `TestReloadAfterReadyAppliesValidAndRejectsInvalidLayers`. | Recorded, run 44860891149 (job 136713767008) at `42d0d7d`: both pass. | Recorded, run 44860891149 (job 136713766919) at `42d0d7d`: both pass. |

### Scenarios

`DH-E-EXEC-1`, `DH-E-HOST-4`, `DH-E-BOOT-1`, `DH-E-UTIL-1` (this stage) and
`DH-E-LOADER-1`, `DH-E-BOOT-2` (Stage 6.5) are authored, reviewed in their PRs and
indexed in `test/harness/acceptance-index.yaml`; none is executed in this story.

| ID | File (`src/harness/internal/testkit/scenarios/`) | Executing stage |
| --- | --- | --- |
| `DH-E-EXEC-1` | `exec_tree_scenario_test.go` (`TestLinuxWholeProcessTreeIsKilled`) | DH-S-18 (step 18.3) |
| `DH-E-HOST-4` | `host_report_scenario_test.go` (`TestDyingHostLeavesReadableReport`) | DH-S-18 (step 18.2) |
| `DH-E-BOOT-1` | `boot_first_page_scenario_test.go` (`TestFailedLaunchFailsLoudlyAndServingOneServes`) | DH-S-22 (step 22.3) |
| `DH-E-UTIL-1` | `egress_proxy_scenario_test.go` (`TestAllTrafficGoesThroughInterceptingProxy`) | DH-S-39 (step 39.15) |
| `DH-E-LOADER-1` | `loader_edit_scenario_test.go` | DH-S-17 (step 17.7) |
| `DH-E-BOOT-2` | `boot_two_process_scenario_test.go` | DH-S-21 (step 21.13) |
| `DH-E-EXEC-10` | `exec_scope_ssh_scenario_test.go` (`TestLinuxSSHLoginDelegatedScopeOwnsTree`) | DH-S-18 (step 18.3) |
| `DH-E-STORAGE-6` | `storage_private_scenario_test.go` (`TestAnotherLocalUserCannotReadStoredData`) | DH-S-18 (step 18.10) |

**Scenario line (gap-closure 13.6.7, 13.6.8; M1-G-CLOSE-7).** DH-E-EXEC-10 and
DH-E-STORAGE-6 are owed by DH-S-6 under rule 4: impl.md sec. 7.5 places them in
DH-S-18, the paired acceptance stage of DH-S-6. They were not written in Phase 6.

- **Authored** in Stage 13.6 (commit `0b5d058`): `//go:build scenario`, one
  `//dsh:scenario` directive each.
- **Inputs** are `DSH_SCENARIO_EXEC10_*` and `DSH_SCENARIO_STORAGE6_*`. A missing
  input fails the test, naming the variable; it never skips.
- **Indexed** under `DH-S-18` with `authored: DH-S-6` in
  `test/harness/acceptance-index.yaml`.
- **Reviewer:** pending PR review.
- **Not executed** in this story.

Checks run on the files:
- `go -C src/harness vet -tags scenario ./internal/testkit/scenarios/` exits 0 on
  Windows and with `GOOS=linux`.
- The default `go test -list .` lists no scenario.
- `CheckRule4Authoring` named exactly these two before they were indexed, and
  passes after. It now runs in the `scenario-index` gate.

Each file starts with `//go:build scenario`, binds one test with `//dsh:scenario`,
and fails (never skips) when an input is missing or the host is the wrong system.
The four scenarios authored here (`DH-E-EXEC-1`, `DH-E-HOST-4`, `DH-E-BOOT-1`,
`DH-E-UTIL-1`) build `cmd/dsh` from the checkout through the kit's `launcher.Build`
(`dshBinary` in `binary_test.go`) and run that binary; `DH-E-EXEC-1`'s
command-line inputs must run it as `{dsh}`. The two Stage 6.5 scenarios
(`DH-E-LOADER-1`, `DH-E-BOOT-2`) are unchanged here: they run the harness only
through their operator-supplied start commands and build nothing themselves, so
their executing stages (DH-S-17, DH-S-21) bind those commands to the checkout. The
index's `built-through` is
raised to `DH-S-6`, so every ID authored at or before DH-S-6 must have its file.

Every scenario run of the binary is tree-owned: `runDsh` and `startDsh`
(`binary_test.go`) start it through the kit's `launcher.Run` / `launcher.Spawn`, i.e.
`internal/world/subprocess` (Job Object, cgroup or process group). A run that
outlives its bound, or a descendant that still holds its output, has the whole
tree force-stopped within `3*launcher.Grace`. This is covered in the default suite by
`TestRunTimeoutStopsTheTreeHoldingItsOutput` (a hung program whose grandchild holds
its stdout: `Run` returns `ErrTimeout` in about 3.4 s and neither pid is alive).

Termination in `DH-E-BOOT-1` and `DH-E-HOST-4` is the platform's own signal
(`terminate_unix_test.go`, `terminate_windows_test.go`): SIGTERM on POSIX; on
Windows the tree's root is a re-exec of the test binary acting as a console host,
which starts the binary in a hidden console of its own (still inside the Job
Object). That console is then closed, which the Go runtime delivers as `SIGTERM`
(`CTRL_CLOSE_EVENT`), since Windows cannot send SIGTERM to another process. The
mechanism was checked on this Windows 11 runner, tree-owned, against a probe program
that exits 0 on `SIGTERM` and 130 on an interrupt: 10 runs out of 10 received
`terminated` and exited 0. It was also checked against the `dsh web` built from this
checkout: closing its console made it exit 0.

```text
$ go -C src/harness vet -tags scenario ./...          # exit 0
$ GOWORK=off go -C tools/harness-gate test -count=1 -run 'Scenario|Require' .
ok   github.com/smartpcr/agent-plugin/tools/harness-gate
$ harness-gate require --stage DH-S-18
DH-E-TOOLCHAIN-2,DH-E-CHOKIDAR-1,DH-E-CHOKIDAR-2,DH-E-NODEFS-1,DH-E-RIPGREP-1,DH-E-RIPGREP-2,DH-E-EXEC-1,DH-E-HOST-4
$ harness-gate require --stage DH-S-22
DH-E-BOOT-1
$ harness-gate require --stage DH-S-39
DH-E-UTIL-1
```

The default gate passes `-tags=` and so executes none of them.

### Held open

- **H-21**: step 6.12's `dsh config` commands wait for the admin tree of DH-S-16;
  the preview, explain and check functions are built and tested, the commands are not.
  H-21 closing step: impl.md 16.10 (`dsh config show`, `dsh config validate`).

### Linux ownership fallback (TC-X2, U-K1-1)

On Linux, `world/subprocess` owns a tree through a child control group when the
host's own cgroup v2 is writable. Otherwise it falls back to the process group and
reports it: `Owner{Kind: ProcessGroup, Exact: false, Fallback: "own control group
not writable; delegated scope not requested (U-K1-1)[: cause]"}`
(`owner_linux.go`), which the bash tool's result shows as `owner: process-group`
with one warning. `TestTreeOwnerIsReported` checks the reported owner on each system.
Reported in run 44860891149 at `42d0d7d`: on the `ubuntu-latest` runner the fallback
was taken, `owner process-group (fallback: own control group not writable; delegated
scope not requested (U-K1-1))`, and the whole tree was still stopped in 2.01 s.
macOS (historical) reports `process-group` and Windows `job-object`. The cgroup path itself (a
writable delegated cgroup) and the fallback under `DH-E-EXEC-1` are exercised in
DH-S-18 (`DH-E-EXEC-1` step 3).

## Deviations, recorded

- The fake transport is an `http.RoundTripper` used by the kit's own `llm.Adapter`.
  It is not routed through `util/egress`, which has no transport seam; an adapter that
  dials through egress swaps its round tripper for the kit's in the same way.
- The scenario inputs (`DSH_SCENARIO_*`) and the output wording they match
  (`stop: timeout`, `owner: cgroup`, the report path on standard error, a
  `token=` URL at readiness) are the area documents' wording; the executing stage
  may adjust them to the commands it builds.
- `host/sharing/component.go` gained its missing final newline: the repository
  gate's `gofmt` step failed on it at `f394c24`.
- The repository gate's analyzer `vet` step still reports findings in tests that
  predate this stage (`src/go/cordis`, `llm`, `session`, `human`, the `util` fuzz
  seeds, and `boot`/`util/egress` lines this stage did not write); none is in a file
  this stage authored.

M1 gate repair (plan 13.2.4; [DH-S-1.md, "M1 gate repair"](DH-S-1.md#m1-gate-repair)): `internal/boot/audit_test.go` and `boot_test.go` start every `Run` through `runJoined` (P-C: the cleanup stops the app, then joins `Run`, bounded), and the `TestStuckReloadDrainSharesTheShutdownBound` cleanup waits on `clock.Manual.BlockUntil` for a pending timer before each `Advance` instead of sleeping; TC ids and assertions unchanged.

## Evidence record (impl.md sec. 7.3)

These are the eight fields of [impl.md sec. 7.3](../../../harness/impl.md#73-the-evidence-record),
in the format of [the evidence runbook](README.md#the-evidence-record-headings). Plan step
18.1.7 fills them at the frozen M1 closure commit; plan Stage 14.6 adds its "Gap closure
(14.6)" section before that. Until then each field points to the history above, which
stays exactly as it was recorded and is not closure evidence (M1-D-CLOSE-1).

### Source and build identity

At closure: not yet recorded (plan 18.1.7). Historical: commits `f394c24` (Stages 6.1 to
6.6), `42d0d7d` (the manual-check run) and `2302fc8` (the confirming run), and the
`src/sdk` pin of `src/harness/go.mod`, under "Stage 6.7" and "Manual checks" above.

### Environment and fixture

At closure: both local lanes, from their run records, with the tree owner each reports
(`job-object` on Windows; on WSL the `owner:` line with `systemd=true`, and without it
while plan 19.4 has not landed, EN-5). macOS: out of scope (`platforms-windows-wsl`).
Historical: Windows 11 locally, and hosted `ubuntu-latest`, `macos-latest` and
`windows-latest` runners; the macOS results are historical.

### Exact tests and commands

At closure: the lane scripts, and `go -C src/harness test -race -count=1 ./...`. The
scenario value of `DSH_REQUIRE_SCENARIOS` stays empty: no DH-S-6 scenario executes in
this story. Historical: the commands under "Tests" and "DH-S-6 exit checks" above.

### Run identifiers

At closure: the run-record ids of both lanes, in the runbook's template (plan 18.1.3).
WSL lane: not run. Windows lane: not run. macOS: out of scope (`platforms-windows-wsl`).
Historical: `harness-manual-checks` runs 44860891149 and 44861208866 and their job ids;
their macOS jobs are historical.

### Results

At closure: not yet recorded. Historical: "Tests that waited on a later step of circle
C-1" and the test table of Stage 6.7.

### Manual checks

impl.md DH-S-6 names three checks: boot a tree with a broken required entry and record
the single fatal message and the report file (steps 6.10, 6.11); start a child that
ignores termination and record that the tree is gone within the bound, on each operating
system (steps 6.2, 6.3); edit a layer file badly and record that the running tree is
unchanged (steps 6.13, 6.14). Plan step 14.6.12 re-records them on Windows native and
Linux under WSL ([plan Stage 14.6](../gap-closure-plan.md#stage-146-dh-s-6-m1-blockers)).
The other steps take the rule-3 default. Historical record: "Manual checks (impl.md
DH-S-6; `manual`)" above, whose macOS column is historical. How to run and fill the
register: [the evidence runbook](README.md#manual-checks). Composed tests run as
`go -C src/harness test -count=1 -v -run '<names>' <packages>`.

#### Manual checks register

| Step | Check | Procedure | Systems | Operator | Commit | Seen | Label |
| --- | --- | --- | --- | --- | --- | --- | --- |
| 6.1 | rule-3 default: leaf packages (errors, identifiers, home location, media types) | composed test: `./internal/util/herr/ ./internal/util/ident/ ./internal/util/homedir/ ./internal/util/mediatype/` | one lane | | | | `manual` |
| 6.2 | a child that ignores termination: started through the one spawn package | composed test: `TestStopTreeStopsAndAwaitsTheTree`, `TestTreeOwnerIsReported` in `./internal/world/subprocess/`; record the `owner` line | Windows, WSL | | | | `manual` |
| 6.3 | a child that ignores termination: the whole tree is gone within the bound | composed test: `TestStopTreeStopsAndAwaitsTheTree`, `TestContextEndStopsDescendantsAfterChildExit` in `./internal/world/subprocess/`; record `stopped in <time>` and that no pid is alive. WSL: with `systemd=true`, and without it while plan 19.4 has not landed | Windows, WSL | | | | `manual` |
| 6.4 | rule-3 default: launch facts, launch environment and expression scope | composed test: `./internal/boot/launch/` | one lane | | | | `manual` |
| 6.5 | rule-3 default: the storage directories exist, are private and are checked at start | composed test: `./internal/storage/home/` (run as a non-root, non-elevated user) | Windows, WSL | | | | `manual` |
| 6.6 | rule-3 default: configuration names a secret; the value sits behind one store | composed test: `./internal/human/secret/` | one lane | | | | `manual` |
| 6.7 | rule-3 default: profiles, embedded bundles and the layer stack | composed test: `./internal/boot/profile/` | one lane | | | | `manual` |
| 6.8 | rule-3 default: one command tree, help and usage errors | composed test: `./internal/cli/ ./internal/cli/cmdline/`; by hand: `dsh --help` and one usage error, recording the exit status | one lane | | | | `manual` |
| 6.9 | rule-3 default: every command prints and exits by one contract | composed test: `./internal/cli/output/` | one lane | | | | `manual` |
| 6.10 | a broken required entry: one fatal message and one report file | composed test: `TestFatalPrintsOneMessageAndWritesOneReport` in `./internal/host/diag/`; record the message and the report path | Windows, WSL | | | | `manual` |
| 6.11 | a broken required entry fails boot once | composed test: `TestBrokenRequiredEntryFailsBootOnce` in `./internal/boot/`; record the one message naming the entry, the exit status and the one report under `reports/` | Windows, WSL | | | | `manual` |
| 6.12 | rule-3 default: the composed tree is previewed, explained and checked without starting it | composed test: `./internal/boot/config/` (the `dsh config` commands are held open as H-21 until impl.md 16.10) | one lane | | | | `manual` |
| 6.13 | a bad layer edit: the running tree is unchanged | composed test: `TestBadLayerEditLeavesRunningTreeUnchanged`, `TestReloadAfterReadyAppliesValidAndRejectsInvalidLayers` in `./internal/boot/...`; record the rejection's file, line and column and the unchanged start count | Windows, WSL | | | | `manual` |
| 6.14 | a bad settings edit changes nothing | composed test: `TestRefusedEditsChangeNothing`, `TestDescribeShowsEffectiveValueAfterRejection` in `./internal/storage/settings/` | Windows, WSL | | | | `manual` |
| 6.15 | rule-3 default: nothing leaves by default; one off-switch per kind | composed test: `./internal/host/sharing/` | one lane | | | | `manual` |
| 6.16 | rule-3 default: every outbound connection follows one policy | composed test: `TestNoPermitNoDial`, `TestWithdrawnPermitCancelsAndRefuses` in `./internal/util/egress/` | one lane | | | | `manual` |
| 6.17 | rule-3 default: the test kit runs real components against scripted boundaries | composed test: `TestVirtualClockDrivesDeadlinesWithoutSleeping`, `TestFakeModelScriptsATurn`, `TestLaunchedProgramAndHomeTornDownByCleanup` in `./internal/testkit/...` | one lane | | | | `manual` |

### Cleanup evidence

At closure: no pid of a stopped tree is alive (a process listing after rows 6.2 and 6.3)
and every temporary home is gone; `git status --porcelain` empty after the run.
Historical: the "no pid alive" observations in the manual-check table above.

### Open items

At closure: plan 18.1.11 lists them. Now open: H-21 ("Held open" above, closing step
impl.md 16.10); the DH-S-6 M1 blockers of plan Stage 14.6 and the decisions M1-D-BOOT-1,
-2 and -3; the scenarios authored here, which run in DH-S-18, DH-S-21, DH-S-22 and
DH-S-39.
