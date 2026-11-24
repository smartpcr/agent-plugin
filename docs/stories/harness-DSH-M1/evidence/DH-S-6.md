# DH-S-6 evidence

Decisions governing this record ([decisions.md](../decisions.md)): `platforms-windows-wsl` (the macOS rows below are historical), M1-D-CLOSE-1 (ticks made while the gate was red are provisional until the closure commit), and M1-D-BOOT-1 (b), M1-D-BOOT-2 (b) and M1-D-BOOT-3 (A), in force since 2026-11-22: the cut-short parts of seven deliverables and `DH-T-HOST-17` are held open (H-24 to H-32, ["Held open"](#held-open)), and the usage-error status is 64. Logged by gap-closure-plan Stage 12.1; merge commit: recorded when the Stage 12.1 change merges.

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
seventeen deliverables are built at that commit. _Corrected (gap-closure 14.6.11,
M1-D-BOOT-1 (b)): seven of them, steps 6.2, 6.3, 6.6, 6.7, 6.15, 6.16 and 6.17, were
cut short when the story plan was written. They stay ticked as built, and each cut
part is held open with its closing step under ["Held open"](#held-open)._

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
# Linux runnervm0hi8g 7.0.0-1020-azure x86_64, go1.26.0 linux/amd64 (job 136713767008) -- hosted ubuntu-latest, not an M1 lane
subprocess_test.go:88: owner process-group (fallback: own control group not writable; delegated scope not requested (U-K1-1)); stopped in 2.011083403s
--- PASS: TestStopTreeStopsAndAwaitsTheTree (2.02s)
--- PASS: TestContextEndStopsTheTree (0.00s)
--- PASS: TestContextEndStopsDescendantsAfterChildExit (0.01s)
--- PASS: TestTreeOwnerIsReported (0.00s)
--- PASS: TestRunTimeoutStopsTheTreeHoldingItsOutput (2.39s)
--- PASS: TestLaunchedProgramAndHomeTornDownByCleanup (0.18s)

# Darwin 25.6.0 arm64, go1.26.0 darwin/arm64 (job 136713766919) -- historical, platforms-windows-wsl
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
136714770420 Linux (hosted `ubuntu-latest`, not an M1 lane), 136714770672 macOS (historical), 136714770648 Windows). Linux: `owner
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

| Check | Windows 11 | Linux (hosted `ubuntu-latest`; not an M1 lane, superseded by ["Gap closure (14.6)"](#gap-closure-146)) | macOS (historical, `platforms-windows-wsl`) |
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

A held-open entry is a part of a deliverable that is ticked as built but is not done:
the part that waits is not enabled until its closing step lands. H-21 is impl.md's own
entry (sec. 10.3). H-24 to H-32 are the M1 story's entries: decision M1-D-BOOT-1 (b)
holds each part that the story plan cut from a DH-S-6 deliverable open until the stage
that builds it before its first M2 consumer, and M1-D-BOOT-2 (b) holds `DH-T-HOST-17`
open. impl.md carries pointer lines to this list (DC-2); its table is unchanged. Each
closing stage records the entries it closes in its own section of this file.
Decision M1-D-CLOSE-5 defers each `DH-T` ID of these entries that no M1 code proves
to the DH-S stage, at or before DH-S-18, that builds or first needs it, by a
`//dsh:test <ID> later DH-S-<n>` directive in a package `doc.go`; the Closing step
column names that stage, and impl.md's section of the stage names the ID (DC-2). A
deferred ID stays open and is never counted done.

- **H-21** (open): step 6.12's `dsh config` commands wait for the admin tree of DH-S-16;
  the preview, explain and check functions are built and tested, the commands are not.
  H-21 closing step: impl.md 16.10 (`dsh config show`, `dsh config validate`).

| ID | Step | Deliverable | The part held open | Gap | Closing step | Status |
| --- | --- | --- | --- | --- | --- | --- |
| H-24 | 6.2 | DH-D-EXEC-1 | Run and capture with a cap (`Run`), private channels, a detached launch, the confirmation helper, creation inside the Windows job in one call, and `Stdin`, `OutputMode` and a typed `Launch` on `Spec` | M1-G-BOOT-2 | gap-closure 19.3.1 to 19.3.6 (the batch-file opt-in moved to 16.5.1); tests `DH-T-EXECA-1` to `-3` and `DH-T-NODEPROC-8` deferred `later DH-S-13` (M1-D-CLOSE-5) | open |
| H-25 | 6.3 | DH-D-EXEC-2 | The delegated Linux scope from the user service manager, the `run/owners.json` record of live owners, and the five-rung stop ladder in place of the two-rung `StopTree` | M1-G-BOOT-3 | gap-closure 19.4.1 to 19.4.3; test `DH-T-EXEC-6` deferred `later DH-S-13` (M1-D-CLOSE-5) | open |
| H-26 | 6.3 | DH-D-EXEC-2 | The trusted or untrusted mark of each tree, `PeerTree` and `Limits` | M1-G-BOOT-4 | gap-closure 19.4.4, 19.4.5; test `DH-T-EXEC-17` deferred `later DH-S-13` (M1-D-CLOSE-5) | open |
| H-27 | 6.6 | DH-D-HUMAN-7 | Managed secret-store writes, the `.env` source, leases with a revocation signal (the lease half of `DH-T-HUMAN-12`) and the source checks | M1-G-BOOT-7 | gap-closure 19.5.4 | open |
| H-28 | 6.7 | DH-D-BOOT-3 | `profile reset` (rename to a backup, never delete) and the safe-mode launcher layer, so `--safe` disables the `plugins` group and the optional bundles | M1-G-BOOT-14 | gap-closure 19.5.5; tests `DH-T-BOOT-8` and `DH-T-BOOT-23` deferred `later DH-S-16` (M1-D-CLOSE-5) | open |
| H-29 | 6.15 | DH-D-HOST-4 | The recorded proof that nothing else leaves: a recording transport and resolver that see every outbound request and name lookup (`testkit/egresstest`) | M1-G-BOOT-9, -11 | gap-closure 19.5.2; its tests are `DH-T-UTIL-15` (H-31, deferred `later DH-S-12`) and `DH-T-HOST-17` (H-32, deferred `later DH-S-16`) (M1-D-CLOSE-5) | open |
| H-30 | 6.16 | DH-D-UTIL-2 | The child-environment proxy overlay (`egress.ChildEnv`) and the dial guard against private addresses | M1-G-BOOT-10 | gap-closure 19.3.7, 19.3.8; tests `DH-T-UNDICI-7` and `DH-T-UNDICI-9` deferred `later DH-S-13` (M1-D-CLOSE-5) | open |
| H-31 | 6.17 | DH-D-UTIL-6 | `testkit/compose`, `testkit/egresstest`, `testkit/faultserver`, the `testing/synctest` clock and the chunk-level scripted adapter | M1-G-BOOT-11 | gap-closure 19.5.1 to 19.5.3; tests `DH-T-UTIL-12`, `-13` and `-15` deferred `later DH-S-12` (M1-D-CLOSE-5) | open |
| H-32 | 6.15 | DH-D-HOST-4 | `DH-T-HOST-17`, a recorded run of each shipped profile; its inputs (the shipped profiles, the replay model) arrive in DH-S-16 and M3 | M1-G-BOOT-9 | impl.md 16.7 (M1-D-BOOT-2 (b)); test `DH-T-HOST-17` deferred `later DH-S-16` (M1-D-CLOSE-5) | open |

### Linux ownership fallback (TC-X2, U-K1-1)

On Linux, `world/subprocess` owns a tree through a child control group when the
host's own cgroup v2 is writable. Otherwise it falls back to the process group and
reports it: `Owner{Kind: ProcessGroup, Exact: false, Fallback: "own control group
not writable; delegated scope not requested (U-K1-1)[: cause]"}`
(`owner_linux.go`). `TestTreeOwnerIsReported` checks the reported owner on each system,
and `TestStopTreeStopsAndAwaitsTheTree` logs it as the `owner` line. (An earlier
version of this record said a bash tool's result shows the owner; no bash tool exists
in M1, so that claim is withdrawn, gap-closure 14.6.11.)
Reported (historical, a hosted runner rather than an M1 lane) in run 44860891149 at `42d0d7d`: on the `ubuntu-latest` runner the fallback
was taken, `owner process-group (fallback: own control group not writable; delegated
scope not requested (U-K1-1))`, and the whole tree was still stopped in 2.01 s.
macOS (historical) reports `process-group` and Windows `job-object`. The cgroup path itself (a
writable delegated cgroup) and the fallback under `DH-E-EXEC-1` are exercised in
DH-S-18 (`DH-E-EXEC-1` step 3).

## Deviations, recorded

- The fake transport is an `http.RoundTripper` used by the kit's own `llm.Adapter`.
  It is not routed through a permit; an adapter that dials through egress swaps its
  round tripper for the kit's in the same way. _Since gap-closure 14.6.7 and 14.6.8:_
  the bare client under `llm/transport.Over` is `egress.Over`, so no HTTP client is
  built outside `util/egress` and `internal/testkit`, and the archtest seam rule allows
  `transport.Over` only in `_test.go` files and `internal/testkit` (see
  ["Gap closure (14.6)"](#gap-closure-146)).
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

## Gap closure (14.6)

Status: built, not done. Steps 14.6.1 to 14.6.6 are committed in `06d21aa` and its
follow-up `2ee09dc`; 14.6.7, 14.6.8, 14.6.9 and 14.6.11 in `0e94ba9`. 14.6.10 (the test
tags and the `DH-T` matrix below) is in the working tree on `2ee09dc`, not yet
committed. The two gate run records are added when the stage closes.

**Decision outcomes.**
- M1-D-BOOT-1 (b): the cut-short parts of steps 6.2, 6.3, 6.6, 6.7, 6.15, 6.16 and 6.17
  are held open as H-24 to H-31 ("Held open" above), with pointer lines in impl.md's
  DH-S-6 section and sec. 10.3 (DC-2).
- M1-D-BOOT-2 (b): `DH-T-HOST-17` is held open as H-32, closing at impl.md 16.7.
- M1-D-BOOT-3 (A): the usage-error status is 64. `cli/output/status.go` sets
  `Usage = 64`; the command reference golden lists 64 and no row 2; the story-plan
  scenario and the e2e feature say "exits 64". `DH-T-CLI-6`: `TestStatusTableMatchesTheAreaSpec`
  (`cli/output`: no status and no error code maps to 2, and the statuses equal
  app-cli.md sec. 6.2.4 with its reserved row 2 left out) and `TestNoCommandPathReturns2`
  (`cli`: every command, profile and hidden mode, bare, with `--help` and with an
  unknown flag; no line exits 2, and an unknown flag of a fixed command exits 64).

**No HTTP client outside egress (14.6.7, 14.6.8; M1-G-BOOT-8).**
- `archtest.HTTPClients` (`internal/archtest/egress.go`, modelled on `spawn.go`) parses
  every non-test file outside `internal/util/egress`, `internal/testkit` and `testdata`,
  and names each `net/http` `Client` or `Transport` value (a literal with or without
  `&`, `new(T)`, a variable or a field; a pointer only refers to a client), each use
  of `DefaultClient` or `DefaultTransport`, and each package-level helper (`Get`,
  `Head`, `Post`, `PostForm`). A dot import of `net/http` is itself a finding.
- `archtest.SeamCalls` allows `llm/transport.Over` only in `_test.go` files and
  `internal/testkit`, including a call from inside `llm/transport`. `Over`'s doc
  comment states the rule.
- One violation in the tree, fixed: `llm/transport/transport.go:75` built
  `&http.Client{...}` inside `Over`. It now calls the new `egress.Over(rt)`, the bare
  client with redirects refused and no permit, which the seam rule allows only in tests,
  the test kit and `internal/llm/transport`. `TestRedirectNotFollowed` covers it.
- Seeded tree `internal/archtest/testdata/egress`: every line marked `// seeded:` is
  reported and nothing else, among them `&http.Client{...}` in a sender and a non-test
  call to `transport.Over` (the stage scenario "A client literal outside egress fails
  the gate").
- Tests: `TestNoHTTPClientOutsideEgress`, `TestSeededTreeFailsTheEgressRules`
  (`DH-T-UNDICI-8`, `DH-T-HOST-19`), `TestNoTestSeamOutsideTests` (`DH-T-HOST-19`),
  `TestEgressRulesExemptions` (`DH-T-UNDICI-8`), and in `host/sharing`
  `TestEverySharingClientBelongsToAListedSender` (`DH-T-HOST-19`: a permit for an
  unregistered kind is refused even when configured on; every client egress hands out
  belongs to a listed sender and goes to its endpoint).

Local checks of these steps (Windows native, go1.27.4, working tree on `e77d26b`):
`gofmt` clean; harness-gate vet (tool built from `git archive HEAD tools/harness-gate`)
clean with `-tags=`, `-tags=replay,scenario` and `-tags=e2e`, and `go vet` with
`GOOS=linux`; staticcheck clean; `-race -shuffle=on -count=3` passes for `archtest`,
`cli/...`, `util/egress`, `llm/transport` and `host/sharing`; coverage `archtest` 97.8%,
`cli/output` 90.5%. These are local evidence, not gate run records.

**Manual checks re-recorded (14.6.12, `manual`).** At `2ee09dc543caa4776e4d886e219e2e72b448af56` (HEAD; a clean `git archive` export), on both M1 lanes; the rows
are in the register below. This commit holds every 14.6 change except the 14.6.10 tags,
which change comments only.
- Windows native: Windows 11 Enterprise 10.0.28000, go1.27.4, medium integrity (not elevated).
- Linux under WSL: WSL Ubuntu 24.04.5, kernel 6.18.58.1-microsoft-standard-WSL2, ext4, go1.27.4, non-root, `systemd=true`. The login shell's control group is `/non-systemd`, so a tree
  takes the reported process-group fallback; inside a delegated user scope
  (`systemd-run --user --scope -p Delegate=yes`) the owner is `control-group`.
- Without `systemd=true`: not run. It needs a `/etc/wsl.conf` change and
  `wsl --shutdown`, which would stop every other session in this shared distribution.
  No code at this commit asks for a scope (U-K1-1), so the expected owner is the same
  process-group fallback as the login shell's; that expectation is not a record.
- Delegated scope: `TestContextEndStopsDescendantsAfterChildExit` failed once in the
  record run ("grandchild <pid> alive after TreeGone"), and 2 times in 40 on a re-run, at
  this commit and equally at `e77d26b`. It passed 40 of 40 in the login shell
  (process-group rung). So the control-group row of 6.3 is not a pass; it is registered
  as [M1-G-BOOT-36](../gap-analysis.md).
- No check needed a person's input.
- Cleanup: the WSL export (`~/src/m1467-2ee09dc`) was removed; the clone
  `~/src/agent-plugin` was not used by these checks; no `subprocess.test` process or
  `run-*` user scope is left.

_Superseded record (kept as history, not current):_ the rows were first recorded at
`e77d26b9e3954e3ccba6b6c27954703a60ed641d` (`e77d26b`), before the 14.6 changes were committed: the same tests passed on
both lanes, with `owner job-object; stopped in 2.0066318s` on Windows, the process-group
fallback (`stopped in 2.01061999s`) in the WSL login shell and `owner control-group;
stopped in 2.012320283s` in a delegated scope, and `dsh --no-such-flag` exited 2. The
rows below replace them.

**Test tags and the DH-T matrix (14.6.10).** Every test of the 13 packages
(`world/subprocess`, `storage/home`, `human/secret` with `redact`, `boot/profile`,
`cli` with `cli/cmdline`, `cli/output`, `host/diag`, `boot`, `storage/settings`,
`host/sharing`, `util/egress`, `testkit/*`, `archtest`) that proves a clause of a
DH-S-6 Done-when ID now carries a `//dsh:test` directive. The same was done in
`boot/config` and `boot/launch` (steps 6.4, 6.12, 6.13). Only comment lines changed, and
each tagged package's tests pass once on Windows. A tag means the test proves at least
one clause of the ID; where it proves only part of one, the matrix says so and names
the part that is open.

Not tagged:
- **69 tests that prove no Done-when ID.** Helpers of the kit and their own tests:
  `subprocesstest` (10), `procwait` (6), `temphome` (5), `answerer` (5), `launcher` (7),
  `model` (6), the kit tree and buffer (2), `clock` (1), and 3 golden-helper tests.
  Also storage path and sync tests (6), settings form listings (6), and single tests
  in `world/subprocess` (4), `human/secret` (2), `boot/profile`, `cli`, `boot` audit,
  `host/sharing` (2) and `archtest` (`TestParse`). Giving them an ID they do not prove
  would be a false tick.
- **The 34 `testkit/scenarios` tests.** Each is bound by `//dsh:scenario` to a DH-E
  scenario, not to a DH-T row.

`GOWORK=off go -C tools/harness-gate run . trace-tests --root ../.. --stage DH-S-6`:
before the tags, DH-S-6 had required 81, tagged 16, missing 65. After them it has
required 81, tagged 63, deferred 0, missing 18. Each missing ID is a gap,
[M1-G-BOOT-18](../gap-analysis.md) to -35, never a tick; most are held open.

| Step | ID | Tests (package: test) | Status |
| --- | --- | --- | --- |
| 6.1 | `DH-T-MIME-1` | `util/mediatype`: `TestTable` | tagged |
| 6.1 | `DH-T-MIME-2` | `util/mediatype`: `TestProcessTableCannotChangeAnswers` | tagged |
| 6.1 | `DH-T-NODECRYPTO-3` | `util/ident`: `TestNew` | tagged |
| 6.1 | `DH-T-CODEFRAME-1` | `boot/config`: `TestDiagnoseFramesAYAMLSyntaxError`, `boot/config`: `TestReportDiagnosticsFrameEachIssue`, `util/herr`: `TestFrame` | tagged |
| 6.1 | `DH-T-UTIL-8` | `util/herr`: `TestKindInnermostAndNoSecret`, `util/herr`: `TestReasonDeclarations`, `util/homedir`: `TestResolveOrder`, `util/mediatype`: `TestLanguage`, `world/subprocess`: `TestReasonsAreDeclared` | tagged |
| 6.1 | `DH-T-UTIL-9` | `util/retain`: `TestTextWindowsNeverSplitARune`, `util/retain`: `TestTextWindowsAndNotice`, `util/retain`: `TestTextLeavesInvalidInputAlone`, `util/retain`: `TestTextHoldsOnlyItsBudget`, `util/retain`: `TestItemsKeepOrder`, `util/retain`: `TestModeNames` | tagged |
| 6.1 | `DH-T-UTIL-11` | `archtest`: `TestImportDirection`, `archtest`: `TestParse`, `archtest`: `TestRulesCatchViolations` | tagged |
| 6.2 | `DH-T-EXEC-1` | `archtest`: `TestParse`, `archtest`: `TestNoSpawnOutsideSubprocess`, `archtest`: `TestSpawns` | tagged |
| 6.2 | `DH-T-EXEC-2` | `world/subprocess`: `TestLookupEdges`, `world/subprocess`: `TestBatchRefusedThroughNameDecorations`, `world/subprocess`: `TestArgvRules`, `world/subprocess`: `TestWindowsNameRules` | tagged |
| 6.2 | `DH-T-EXEC-3` | `human/secret`: `TestDefaultDenyCoversEveryCredentialShape`, `testkit/launcher`: `TestStartDropsTheStoresSecretNames`, `world/subprocess`: `TestEnvEntryShapes`, `world/subprocess`: `TestChildEnvironmentHoldsNoSecret`, `world/subprocess`: `TestBuildEnv`, `world/subprocess`: `TestBuildEnvRemovesSecretNames`, `world/subprocess`: `TestChildNeverInheritsANamedSecret` | tagged |
| 6.2 | `DH-T-NODEPROC-1` | `world/subprocess`: `TestArgumentsArriveLiterally` | tagged; partial, open: the generated set of random arguments |
| 6.2 | `DH-T-NODEPROC-2` | -- | missing: [M1-G-BOOT-18](../gap-analysis.md) (batch opt-in, gap-closure 16.5.1) |
| 6.2 | `DH-T-NODEPROC-3` | `world/subprocess`: `TestLookupUsesTheChildPath` | tagged |
| 6.2 | `DH-T-NODEPROC-4` | `world/subprocess`: `TestChildEnvironmentHoldsNoSecret`, `world/subprocess`: `TestChildNeverInheritsANamedSecret` | tagged |
| 6.2 | `DH-T-NODEPROC-10` | `archtest`: `TestParse`, `archtest`: `TestNoSpawnOutsideSubprocess`, `archtest`: `TestSpawns` | tagged |
| 6.2 | `DH-T-WHICH-1` | `world/subprocess`: `TestLookupUsesTheChildPath` | tagged |
| 6.2 | `DH-T-EXECA-1` | -- | missing: [M1-G-BOOT-19](../gap-analysis.md) (H-24, gap-closure 19.3.1) |
| 6.2 | `DH-T-EXECA-2` | -- | missing: [M1-G-BOOT-20](../gap-analysis.md) (H-24, gap-closure 19.3.1) |
| 6.2 | `DH-T-EXECA-3` | -- | missing: [M1-G-BOOT-21](../gap-analysis.md) (H-24, gap-closure 19.3.1) |
| 6.2 | `DH-T-CROSSSPAWN-1` | -- | missing: [M1-G-BOOT-22](../gap-analysis.md) (H-24 and the batch opt-in (gap-closure 16.5.1)) |
| 6.3 | `DH-T-EXEC-4` | `world/subprocess`: `TestControlGroupOwnerInADelegatedScope`, `world/subprocess`: `TestInScopeOwnerIsAControlGroup`, `world/subprocess`: `TestInScopeStartFailureLeavesNoGroup`, `world/subprocess`: `TestOwnerString`, `world/subprocess`: `TestCgroupStartErrorBlamesOnlyTheGroup`, `world/subprocess`: `TestCgroupOwnerFallsBackToAProcessGroup`, `world/subprocess`: `TestTreeOwnerIsReported` | tagged; partial, open: the fake user manager and the one scope request (H-25) |
| 6.3 | `DH-T-EXEC-5` | `world/subprocess`: `TestInScopeSurvivorsNameTheGroup`, `world/subprocess`: `TestTreeGoneReportsSurvivors`, `world/subprocess`: `TestStopAllReturnsAtTheContextEnd`, `world/subprocess`: `TestContextEndStopsTheTree` | tagged; partial, open: the five-rung ladder (H-25) |
| 6.3 | `DH-T-EXEC-6` | -- | missing: [M1-G-BOOT-23](../gap-analysis.md) (H-25, gap-closure 19.4.3) |
| 6.3 | `DH-T-EXEC-17` | -- | missing: [M1-G-BOOT-24](../gap-analysis.md) (H-26, gap-closure 19.4.4, 19.4.5) |
| 6.3 | `DH-T-NODEPROC-5` | `testkit/launcher`: `TestRunTimeoutStopsTheTreeHoldingItsOutput`, `world/subprocess`: `TestAwaitDoesNotSpinAfterChildExit`, `world/subprocess`: `TestInScopeStopTreeStopsTheTreeAndRemovesItsGroup`, `world/subprocess`: `TestStopTreeStopsAndAwaitsTheTree`, `world/subprocess`: `TestContextEndStopsDescendantsAfterChildExit` | tagged |
| 6.3 | `DH-T-NODEPROC-6` | -- | missing: [M1-G-BOOT-25](../gap-analysis.md) (no plan step) |
| 6.3 | `DH-T-NODEPROC-7` | `world/subprocess`: `TestGroupRungAsksForTheDeathSignal`, `world/subprocess`: `TestKilledHostTakesItsDirectChild` | tagged |
| 6.3 | `DH-T-NODEPROC-8` | -- | missing: [M1-G-BOOT-26](../gap-analysis.md) (H-24, gap-closure 19.3.5) |
| 6.4 | `DH-T-BOOT-4` | `boot/launch`: `TestEnvPrecedence`, `boot/launch`: `TestDiscoveredFileCannotSetLaunchOnlyName` | tagged |
| 6.4 | `DH-T-BOOT-5` | `boot`: `TestBootOrderAndAuditOnFakes`, `boot`: `TestStopDuringStartupIsNeverReady`, `boot/launch`: `TestBuildHomePrecedence` | tagged |
| 6.4 | `DH-T-BOOT-6` | `boot/launch`: `TestLaunchFactsFeedExpressionScope` | tagged |
| 6.5 | `DH-T-STORAGE-4` | `storage/home`: `TestHomeCreatedPrivate`, `storage/home`: `TestEnsureRefusesLoosenedExistingHome`, `storage/home`: `TestEnsureCreatesMissingParents`, `storage/home`: `TestCheckMissingAndNotDirectory`, `storage/home`: `TestCheckDatabaseFile`, `storage/home`: `TestPrivateDirAndFile`, `storage/home`: `TestEnsurePrivateDirRefusals`, `storage/home`: `TestCheckPrivateOfALoosenedFile`, `storage/home`: `TestCheckPrivateRefusesASymlink`, `storage/home`: `TestCheckRequiresOwnerRWXOnDirectories`, `storage/home`: `TestCheckRequiresOwnerReadOnFiles`, `storage/home`: `TestCheckPrivateRefusesAnInheritingDirectory`, `storage/home`: `TestCheckPrivateRefusesANullAccessList` | tagged |
| 6.6 | `DH-T-HUMAN-10` | `human/secret`: `TestDirectPrintedFormsAreRedacted`, `human/secret`: `TestZeroRefAndMustParse`, `human/secret`: `TestFileSourceRefusesDirectoryAndOversizedFile`, `human/secret`: `TestSecretResolvedPerUseAndRedacted`, `human/secret`: `TestValueNeverPrints`, `human/secret`: `TestConfigRefusesValue`, `human/secret`: `TestUseErrorFormatsMaskedForValueErrors` | tagged; partial, open: the golden file, the template forms and `.env` parse errors (H-27) |
| 6.6 | `DH-T-HUMAN-11` | `human/secret`: `TestSourcesWithNothingToGive`, `human/secret`: `TestResolveOrderAndSources` | tagged; partial, open: stored values, `Unset`, revisions, two-process writes and remote handlers (H-27) |
| 6.6 | `DH-T-HUMAN-12` | `boot`: `TestGuardsMaskTheStoresSecrets`, `human/secret`: `TestMaskCoversBytesAndTheQuotedForm`, `human/secret`: `TestNamedSecretsReachNoChildAndNoLog`, `human/secret`: `TestMaskAndSecretNamesFailClosed`, `human/secret`: `TestIsSecretName`, `human/secret/redact`: `TestShaperMasksReleasedValues`, `human/secret/redact`: `TestMountAndComponent`, `human/secret/redact`: `TestMountRefusesARegistryWithoutAFinalSeat` | tagged |
| 6.7 | `DH-T-BOOT-7` | `boot/profile`: `TestLayerStackPrecedence`, `boot/profile`: `TestStackOrderAndOneWritableLayer`, `boot/profile`: `TestStackErrors`, `boot/profile`: `TestLookupUnknown` | tagged |
| 6.7 | `DH-T-BOOT-8` | -- | missing: [M1-G-BOOT-27](../gap-analysis.md) (H-28, gap-closure 19.5.5) |
| 6.7 | `DH-T-BOOT-23` | -- | missing: [M1-G-BOOT-28](../gap-analysis.md) (H-28, gap-closure 19.5.5) |
| 6.8 | `DH-T-CLI-1` | `cli`: `TestUsageErrorExits64`, `cli`: `TestProfileLaunch`, `cli`: `TestExplicitProfileEndsLauncherOptions` | tagged |
| 6.8 | `DH-T-CLI-2` | `cli`: `TestUnknownProfileAndUnwiredLaunch`, `cli`: `TestReservedWordsAreNoProfiles` | tagged; partial, open: refusing a new profile with a reserved name; hidden modes absent from help and completion |
| 6.8 | `DH-T-CLI-3` | `cli`: `TestReferenceIsCurrent` | tagged |
| 6.8 | `DH-T-COMMANDER-1` | `cli`: `TestUsageErrorExits64`, `cli`: `TestProfileLaunch`, `cli/cmdline`: `TestParseStopAtArgs` | tagged |
| 6.8 | `DH-T-COMMANDER-2` | `cli/cmdline`: `TestParseOptionsAfterFreeWords`, `cli/cmdline`: `TestParseEnds`, `cli/cmdline`: `TestGiven`, `cli/cmdline`: `TestParseUsageErrors` | tagged |
| 6.8 | `DH-T-COMMANDER-3` | `cli`: `TestUsageErrorsOfCommands`, `cli`: `TestHelpIsStatus0OnStdout`, `cli`: `TestBareGroupIsUsageError`, `cli/cmdline`: `TestParseHelp`, `cli/cmdline`: `TestHelp` | tagged |
| 6.8 | `DH-T-COMMANDER-4` | `cli`: `TestReferenceIsCurrent` | tagged |
| 6.9 | `DH-T-CLI-5` | `cli`: `TestVersion`, `cli`: `TestJSONUsageErrorWritesErrorDocument`, `cli`: `TestHelpToClosedStdoutFails`, `cli`: `TestProcessLaunchRunsBoot`, `cli/output`: `TestFailText`, `cli/output`: `TestFailJSONEndsResultWithError`, `cli/output`: `TestResultModes`, `cli/output`: `TestClosedResultStream`, `cli/output`: `TestExitStatusIsNotPrintedAgain` | tagged |
| 6.9 | `DH-T-CLI-6` | `cli`: `TestNoCommandPathReturns2`, `cli`: `TestLaunchErrorIsMapped`, `cli/output`: `TestEveryReasonHasAStatus`, `cli/output`: `TestStatusTableIsClosed`, `cli/output`: `TestStatusTableMatchesTheAreaSpec`, `cli/output`: `TestStatus` | tagged |
| 6.10 | `DH-T-HOST-23` | `boot`: `TestBrokenRequiredEntryFailsBootOnce`, `host/diag`: `TestFatalPrintsOneMessageAndWritesOneReport`, `host/diag`: `TestFatalReleaseIsBounded`, `host/diag`: `TestRingKeepsTheTail`, `host/diag`: `TestFatalMasksThroughTheMasker` | tagged |
| 6.10 | `DH-T-HOST-24` | `host/diag`: `TestFatalWithoutWritableReportsStillPrints`, `host/diag`: `TestFatalPrunesToTheNewestReports`, `host/diag`: `TestInstallRemovesAnEmptyCrashFile` | tagged |
| 6.10 | `DH-T-ELECTRON-4` | `host/diag`: `TestFatalPrintsOneMessageAndWritesOneReport` | tagged |
| 6.11 | `DH-T-BOOT-10` | `boot`: `TestUnreadableCertificateStopsTheLaunch`, `boot`: `TestBrokenRequiredEntryFailsBootOnce` | tagged |
| 6.11 | `DH-T-BOOT-11` | `boot`: `TestBrokenRequiredEntryFailsBootOnce` | tagged |
| 6.11 | `DH-T-BOOT-12` | `boot`: `TestShutdownOverrunExits124`, `boot`: `TestFatalShutdownOverrunIsBounded`, `boot`: `TestStopDuringStartupIsNeverReady`, `boot`: `TestStuckReloadDrainSharesTheShutdownBound`, `cli`: `TestProcessLaunchInterrupted` | tagged |
| 6.11 | `DH-T-BOOT-13` | `boot`: `TestFatalShutdownOverrunIsBounded`, `host/diag`: `TestFatalReleaseIsBounded` | tagged |
| 6.12 | `DH-T-BOOT-14` | `boot/config`: `TestPreviewWithoutStart` | tagged; partial, open: the golden dump, loading it back, and the variant without the profile layer |
| 6.13 | `DH-T-BOOT-15` | `boot`: `TestReloadAfterReadyAppliesValidAndRejectsInvalidLayers`, `boot/config`: `TestCheckPositionsBadValues`, `boot/config`: `TestBadLayerEditLeavesRunningTreeUnchanged`, `boot/config`: `TestReloadRejectsInvalidNewEntry`, `boot/config`: `TestCheckPositionsTheMergeThatWroteTheField`, `boot/config`: `TestUnparsableLayerIsToldOncePerContent`, `boot/config`: `TestOmittedConfigIsValidatedToo` | tagged |
| 6.13 | `DH-T-BOOT-16` | `boot/config`: `TestRejectedEditWritesNothing`, `boot/config`: `TestEditWithoutRunningTreeValidatesSchema`, `boot/config`: `TestUnparsableLayerIsToldOncePerContent`, `boot/config`: `TestEditOfComputedFieldNeedsOverwrite`, `boot/config`: `TestApplyRefusesShadowedEdit`, `boot/config`: `TestRequiredEntriesSurviveReloadAndEdit`, `storage/settings`: `TestComputedFieldEditIsRefused` | tagged |
| 6.13 | `DH-T-BOOT-17` | `boot/config`: `TestConcurrentSettingsEditsDoNotLoseAWrite` | tagged |
| 6.13 | `DH-T-BOOT-18` | -- | missing: [M1-G-BOOT-29](../gap-analysis.md) (no plan step) |
| 6.14 | `DH-T-STORAGE-19` | `storage/settings`: `TestApplyRefusesUnknownEntryAndBadJSON`, `storage/settings`: `TestApplyANestedField`, `storage/settings`: `TestApplyThenStaleEdit` | tagged |
| 6.14 | `DH-T-STORAGE-20` | `storage/settings`: `TestApplyRefusesUnknownEntryAndBadJSON`, `storage/settings`: `TestComputedFieldEditIsRefused`, `storage/settings`: `TestApplyThenStaleEdit`, `storage/settings`: `TestRefusedEditsChangeNothing` | tagged |
| 6.14 | `DH-T-STORAGE-21` | `storage/settings`: `TestUnparsableLayerOnDisk`, `storage/settings`: `TestApplyRefusesUnknownEntryAndBadJSON`, `storage/settings`: `TestApplyWhoseWriteFailsIsUnsaved`, `storage/settings`: `TestDescribeListsLiveFieldsWithTheirLayer`, `storage/settings`: `TestDescribeShowsEffectiveValueAfterRejection`, `storage/settings`: `TestDescribeShowsEvaluatedExpression` | tagged; partial, open: the golden file, two services computing one revision, and saving once the file is writable |
| 6.15 | `DH-T-HOST-17` | -- | missing: [M1-G-BOOT-30](../gap-analysis.md) (H-32, impl.md 16.7) |
| 6.15 | `DH-T-HOST-18` | `boot`: `TestAuditOffSwitchListsEveryKindOff`, `boot`: `TestRealAuditReadsOffSwitchFromLaunchEnv`, `boot`: `TestComposedSharingSettingsReachTheAudit`, `host/sharing`: `TestOffSwitchOverridesPermits`, `host/sharing`: `TestSwitchWithdrawsLivePermit`, `host/sharing`: `TestWithdrawnInFlightKeepsEgressReason` | tagged; partial, open: the recorder that shows no further request |
| 6.15 | `DH-T-HOST-19` | `archtest`: `TestParse`, `archtest`: `TestNoHTTPClientOutsideEgress`, `archtest`: `TestNoTestSeamOutsideTests`, `archtest`: `TestSeededTreeFailsTheEgressRules`, `host/sharing`: `TestConfigureWithdrawsChangedKindsOnly`, `host/sharing`: `TestEverySharingClientBelongsToAListedSender` | tagged |
| 6.16 | `DH-T-UNDICI-1` | `util/egress`: `TestResolveProxyNames`, `util/egress`: `TestSocksProxy` | tagged |
| 6.16 | `DH-T-UNDICI-2` | `util/egress`: `TestResolveProxyNames` | tagged |
| 6.16 | `DH-T-UNDICI-3` | `util/egress`: `TestResolveProxyNames` | tagged |
| 6.16 | `DH-T-UNDICI-4` | `util/egress`: `TestResolveProxyNames` | tagged |
| 6.16 | `DH-T-UNDICI-5` | `boot`: `TestUnreadableCertificateStopsTheLaunch`, `util/egress`: `TestExtraCACerts` | tagged |
| 6.16 | `DH-T-UNDICI-6` | `util/egress`: `TestRedirectNotFollowed` | tagged; partial, open: the same-origin redirect policy, the body cap and the failure-class codes |
| 6.16 | `DH-T-UNDICI-7` | -- | missing: [M1-G-BOOT-31](../gap-analysis.md) (H-30, gap-closure 19.3.8) |
| 6.16 | `DH-T-UNDICI-8` | `archtest`: `TestParse`, `archtest`: `TestNoHTTPClientOutsideEgress`, `archtest`: `TestSeededTreeFailsTheEgressRules`, `archtest`: `TestEgressRulesExemptions` | tagged |
| 6.16 | `DH-T-UNDICI-9` | -- | missing: [M1-G-BOOT-32](../gap-analysis.md) (H-30, gap-closure 19.3.7) |
| 6.16 | `DH-T-UTIL-3` | `util/egress`: `TestNoPermitNoDial`, `util/egress`: `TestWithdrawnPermitCancelsAndRefuses`, `util/egress`: `TestRedirectNotFollowed`, `util/egress`: `TestClientsAreReleasedWithTheirOwner` | tagged |
| 6.16 | `DH-T-UTIL-4` | `host/sharing`: `TestSwitchWithdrawsLivePermit`, `host/sharing`: `TestWithdrawnInFlightKeepsEgressReason`, `util/egress`: `TestNoPermitNoDial`, `util/egress`: `TestWithdrawnPermitCancelsAndRefuses`, `util/egress`: `TestAllows` | tagged |
| 6.16 | `DH-T-UTIL-5` | `util/egress`: `TestResolveProxyNames` | tagged |
| 6.17 | `DH-T-UTIL-12` | -- | missing: [M1-G-BOOT-33](../gap-analysis.md) (H-31, gap-closure 19.5.3) |
| 6.17 | `DH-T-UTIL-13` | -- | missing: [M1-G-BOOT-34](../gap-analysis.md) (H-31, gap-closure 19.5.1) |
| 6.17 | `DH-T-UTIL-14` | `testkit/model`: `TestFakeModelScriptsFailures` | tagged |
| 6.17 | `DH-T-UTIL-15` | -- | missing: [M1-G-BOOT-35](../gap-analysis.md) (H-31, gap-closure 19.5.2) |

H-21 stays open.

## Evidence record (impl.md sec. 7.3)

These are the eight fields of [impl.md sec. 7.3](../../../harness/impl.md#73-the-evidence-record),
in the format of [the evidence runbook](README.md#the-evidence-record-headings). Plan step
18.1.7 fills them at the frozen M1 closure commit; plan Stage 14.6 adds its "Gap closure
(14.6)" section before that. Until then each field points to the history above, which
stays exactly as it was recorded and is not closure evidence (M1-D-CLOSE-1).

### Source and build identity

**At the closure commit (plan 18.1.7).** Commit `593ceaa3efd55d41bf0eeaced5ba7e26acc71966`: one commit for the three modules, on branch `m1-phase-18` over `main` 896c760. Go `go1.27.4` (the go floor). The WSL rehearsal built six `dsh` binaries, each with `vcs.revision=593ceaa…` and `vcs.modified=false`; their SHA-256 values are in [wsl-593ceaa-20261123T2122Z-rehearse.txt](runs/wsl-593ceaa-20261123T2122Z-rehearse.txt).

At closure: not yet recorded (plan 18.1.7). Historical: commits `f394c24` (Stages 6.1 to
6.6), `42d0d7d` (the manual-check run) and `2302fc8` (the confirming run), and the
`src/sdk` pin of `src/harness/go.mod`, under "Stage 6.7" and "Manual checks" above.

### Environment and fixture

**At the closure commit (plan 18.1.7).** Windows 11 Enterprise 10.0.28000 native, with `CC` MSYS2 gcc. WSL Ubuntu 24.04.5 LTS, kernel 6.18.58.1-microsoft-standard-WSL2, ext4 clone, gcc 13.3.0. macOS is out of this story (`macos-return-stage`). The manual checks ran on the same workstation (decision `manual-checks-run-by-lead`). Signed-in clients: Claude Code 2.1.312 and GitHub Copilot CLI 1.0.115-7, Windows only. Fixtures: those named by this stage's register rows.

At closure: both local lanes, from their run records, with the tree owner each reports
(`job-object` on Windows; on WSL the `owner:` line with `systemd=true`, and without it
while plan 19.4 has not landed, EN-5). macOS: out of scope (`platforms-windows-wsl`).
Historical: Windows 11 locally, and hosted `ubuntu-latest`, `macos-latest` and
`windows-latest` runners; the macOS results are historical.

### Exact tests and commands

**At the closure commit (plan 18.1.7).** `tools/harness-gate/lanes/win.ps1` and `tools/harness-gate/lanes/wsl.sh` (the gate with `--evidence`; `rehearse` in WSL). `DSH_REQUIRE_SCENARIOS` is unset. For the register: `evidence/runs/manual-593ceaa/run-register.sh` (Windows, and `--os wsl`), with the by-hand steps of `by-hand-checklist.md`; the per-row commands are in this record's register.

At closure: the lane scripts, and `go -C src/harness test -race -count=1 ./...`. The
scenario value of `DSH_REQUIRE_SCENARIOS` stays empty: no DH-S-6 scenario executes in
this story. Historical: the commands under "Tests" and "DH-S-6 exit checks" above.

### Run identifiers

**At the closure commit (plan 18.1.7).** Windows lane `win-593ceaa-20261123T2122Z` ([record](runs/win-593ceaa-20261123T2122Z.txt)) and WSL lane `wsl-593ceaa-20261123T2122Z` ([record](runs/wsl-593ceaa-20261123T2122Z.txt), [rehearse](runs/wsl-593ceaa-20261123T2122Z-rehearse.txt)). Register logs: `register-windows-593ceaa-20261123T2213Z` and `register-wsl-593ceaa-20261123T2237Z` in [runs/manual-593ceaa/](runs/manual-593ceaa/byhand-results.md). The WSL rehearse record was rebuilt from the lane's tee'd output after the original file was deleted by mistake; the gate record's own `sha256:` line verifies it byte for byte.

At closure: the run-record ids of both lanes, in the runbook's template (plan 18.1.3).
WSL lane: not run. Windows lane: not run. macOS: out of scope (`platforms-windows-wsl`).
Historical: `harness-manual-checks` runs 44860891149 and 44861208866 and their job ids;
their macOS jobs are historical.

### Results

**At the closure commit (plan 18.1.7).** Both lanes `PASSED all 12 gates`, with `trace-tests` in the `trace` gate. `trace-tests` for DH-S-1 to DH-S-11 reports required 310, tagged 288, deferred 22, missing 0. This stage's register: see "Manual checks register" (every row PASS or recorded as seen; not run and pending rows are open, never a pass).

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

**Run at the M1 closure commit (plan 18.1.5, 2026-11-23).** The lead Claude Code session ran every row below at `593ceaa3efd55d41bf0eeaced5ba7e26acc71966`, at the owner's instruction of 2026-11-23 ("you should do that"); the owner did not watch these runs. This departs from M1-D-CLOSE-2 A (a named person runs it at the recorded commit, watches it and records it `manual`) and from the runbook rule that signed-in checks are never run from an agent session, and it needs to be recorded as an owner decision. Logs: `register-windows-593ceaa-20261123T2213Z.log`, `register-wsl-593ceaa-20261123T2237Z.log`, `byhand-windows-593ceaa.log`, `byhand-windows-593ceaa-part2.log`, `byhand-wsl-593ceaa.log`, `signedin-dhs9-593ceaa.log`, `signedin-m1-claude-593ceaa.log` and `signedin-m1-copilot-593ceaa.log`. The WSL rehearse record (`wsl-593ceaa-20261123T2122Z-rehearse.txt`) was rebuilt from the lane's tee'd output after the original file was deleted by mistake.

| Step | Check | Procedure | Systems | Operator | Commit | Seen | Label |
| --- | --- | --- | --- | --- | --- | --- | --- |
| 6.1 | rule-3 default: leaf packages (errors, identifiers, home location, media types) | composed test: `./internal/util/herr/ ./internal/util/ident/ ./internal/util/homedir/ ./internal/util/mediatype/` | one lane | lead session (Claude Code), at the owner's instruction of 2026-11-23; not watched by the owner | `593ceaa3efd55d41bf0eeaced5ba7e26acc71966` | PASS. Windows (`register-windows-593ceaa-20261123T2213Z.log`): 14 tests `--- PASS`, 0 FAIL, among them `TestFrame`, `TestNew`, `TestResolveOrder`, `TestTable`; `herr` 4, `ident` 2, `homedir` 4 and `mediatype` 4 tests, every package ok. | `manual` |
| 6.2 | a child that ignores termination: started through the one spawn package | composed test: `TestStopTreeStopsAndAwaitsTheTree`, `TestTreeOwnerIsReported` in `./internal/world/subprocess/`; record the `owner` line | Windows, WSL | lead session (Claude Code), at the owner's instruction of 2026-11-23; not watched by the owner | `593ceaa3efd55d41bf0eeaced5ba7e26acc71966` | PASS on both lanes. Windows (`register-windows-593ceaa-20261123T2213Z.log`): `--- PASS` for `TestStopTreeStopsAndAwaitsTheTree`, `TestTreeOwnerIsReported`; `owner job-object`. WSL (`register-wsl-593ceaa-20261123T2237Z.log`), login shell with `systemd=true`: the same two `--- PASS`; `owner process-group (fallback: own control group not writable; delegated scope not requested (U-K1-1))`. | `manual` |
| 6.3 | a child that ignores termination: the whole tree is gone within the bound | composed test: `TestStopTreeStopsAndAwaitsTheTree`, `TestContextEndStopsDescendantsAfterChildExit` in `./internal/world/subprocess/`; record `stopped in <time>` and that no pid is alive. WSL: with `systemd=true`, and without it while plan 19.4 has not landed | Windows, WSL | lead session (Claude Code), at the owner's instruction of 2026-11-23; not watched by the owner | `593ceaa3efd55d41bf0eeaced5ba7e26acc71966` | PASS on both lanes, in the shell each lane started from. Windows (`register-windows-593ceaa-20261123T2213Z.log`): `--- PASS` for `TestStopTreeStopsAndAwaitsTheTree`, `TestContextEndStopsDescendantsAfterChildExit`; `owner job-object; stopped in 2.0043664s`; afterwards `tasklist` found no `subprocess.test.exe`. WSL (`register-wsl-593ceaa-20261123T2237Z.log`), login shell with `systemd=true`: the same two `--- PASS`; `owner process-group (fallback ...); stopped in 2.010609883s`; afterwards `pgrep: none` and no `run-*` scope left. Not run at this commit: the WSL variants in a delegated scope and without `systemd=true`. | `manual` |
| 6.4 | rule-3 default: launch facts, launch environment and expression scope | composed test: `./internal/boot/launch/` | one lane | lead session (Claude Code), at the owner's instruction of 2026-11-23; not watched by the owner | `593ceaa3efd55d41bf0eeaced5ba7e26acc71966` | PASS. Windows (`register-windows-593ceaa-20261123T2213Z.log`): 10 tests `--- PASS`, 0 FAIL, among them `TestLaunchFactsFeedExpressionScope`, `TestBuildFacts`, `TestEnvPrecedence`, `TestParseDotenv`; `boot/launch` ok. | `manual` |
| 6.5 | rule-3 default: the storage directories exist, are private and are checked at start | composed test: `./internal/storage/home/` (run as a non-root, non-elevated user) | Windows, WSL | lead session (Claude Code), at the owner's instruction of 2026-11-23; not watched by the owner | `593ceaa3efd55d41bf0eeaced5ba7e26acc71966` | PASS on both lanes. Windows (`register-windows-593ceaa-20261123T2213Z.log`), not elevated: 16 tests `--- PASS`, 0 FAIL, among them `TestHomeCreatedPrivate`, `TestEnsureRefusesLoosenedExistingHome`, `TestCheckPrivateRefusesAnInheritingDirectory`; `TestCheckPrivateRefusesASymlink` SKIP (this host cannot create a symbolic link). WSL (`register-wsl-593ceaa-20261123T2237Z.log`), user `dsh` (uid 1000): 17 tests `--- PASS`, 0 FAIL, `TestCheckPrivateRefusesASymlink` among them. | `manual` |
| 6.6 | rule-3 default: configuration names a secret; the value sits behind one store | composed test: `./internal/human/secret/` | one lane | lead session (Claude Code), at the owner's instruction of 2026-11-23; not watched by the owner | `593ceaa3efd55d41bf0eeaced5ba7e26acc71966` | PASS. Windows (`register-windows-593ceaa-20261123T2213Z.log`): 16 tests `--- PASS`, 0 FAIL, among them `TestSecretResolvedPerUseAndRedacted`, `TestNamedSecretsReachNoChildAndNoLog`, `TestValueNeverPrints`, `TestConfigRefusesValue`; `human/secret` ok. | `manual` |
| 6.7 | rule-3 default: profiles, embedded bundles and the layer stack | composed test: `./internal/boot/profile/` | one lane | lead session (Claude Code), at the owner's instruction of 2026-11-23; not watched by the owner | `593ceaa3efd55d41bf0eeaced5ba7e26acc71966` | PASS. Windows (`register-windows-593ceaa-20261123T2213Z.log`): `--- PASS` for `TestBuiltinProfilesCompose`, `TestBuiltinProfileChecksCatchFaults`, `TestLayerStackPrecedence`, `TestStackOrderAndOneWritableLayer`, `TestStackErrors`, `TestBundlesParseAndResolve`, `TestLookupUnknown`. | `manual` |
| 6.8 | rule-3 default: one command tree, help and usage errors | composed test: `./internal/cli/ ./internal/cli/cmdline/`; by hand: `dsh --help` and one usage error, recording the exit status | one lane | lead session (Claude Code), at the owner's instruction of 2026-11-23; not watched by the owner | `593ceaa3efd55d41bf0eeaced5ba7e26acc71966` | PASS. Composed, Windows (`register-windows-593ceaa-20261123T2213Z.log`): 24 tests `--- PASS`, 0 FAIL (`cli` 17, `cli/cmdline` 7), among them `TestUsageErrorExits64`, `TestHelpIsStatus0OnStdout`, `TestParseUsageErrors`. By hand, Windows (`byhand-windows-593ceaa.log`): `dsh --help` exit 0, 34 lines, first line `Usage: dsh [options] [profile\|command...]`, empty stderr; `dsh --no-such-flag` exit 64, empty stdout, stderr `dsh: cli/usage: unknown flag: --no-such-flag` and `Run 'dsh --help' for usage.`. | `manual` |
| 6.9 | rule-3 default: every command prints and exits by one contract | composed test: `./internal/cli/output/` | one lane | lead session (Claude Code), at the owner's instruction of 2026-11-23; not watched by the owner | `593ceaa3efd55d41bf0eeaced5ba7e26acc71966` | PASS. Windows (`register-windows-593ceaa-20261123T2213Z.log`): 9 tests `--- PASS`, 0 FAIL, among them `TestEveryReasonHasAStatus`, `TestStatusTableMatchesTheAreaSpec`, `TestExitStatusIsNotPrintedAgain`; `cli/output` ok. | `manual` |
| 6.10 | a broken required entry: one fatal message and one report file | composed test: `TestFatalPrintsOneMessageAndWritesOneReport` in `./internal/host/diag/`; record the message and the report path | Windows, WSL | lead session (Claude Code), at the owner's instruction of 2026-11-23; not watched by the owner | `593ceaa3efd55d41bf0eeaced5ba7e26acc71966` | PASS on both lanes. Windows (`register-windows-593ceaa-20261123T2213Z.log`) and WSL (`register-wsl-593ceaa-20261123T2237Z.log`): `--- PASS: TestFatalPrintsOneMessageAndWritesOneReport`; no message text or report path was printed under `-v`; the test's one-message, one-report assertions passed. | `manual` |
| 6.11 | a broken required entry fails boot once | composed test: `TestBrokenRequiredEntryFailsBootOnce` in `./internal/boot/`; record the one message naming the entry, the exit status and the one report under `reports/` | Windows, WSL | lead session (Claude Code), at the owner's instruction of 2026-11-23; not watched by the owner | `593ceaa3efd55d41bf0eeaced5ba7e26acc71966` | PASS on both lanes. Windows (`register-windows-593ceaa-20261123T2213Z.log`) and WSL (`register-wsl-593ceaa-20261123T2237Z.log`): `--- PASS: TestBrokenRequiredEntryFailsBootOnce`; no message, exit status or report was printed under `-v`; the test's assertions (one message naming the entry, the exit status, one report under `reports/`) passed. | `manual` |
| 6.12 | rule-3 default: the composed tree is previewed, explained and checked without starting it | composed test: `./internal/boot/config/` (the `dsh config` commands are held open as H-21 until impl.md 16.10) | one lane | lead session (Claude Code), at the owner's instruction of 2026-11-23; not watched by the owner | `593ceaa3efd55d41bf0eeaced5ba7e26acc71966` | PASS. Windows (`register-windows-593ceaa-20261123T2213Z.log`): 32 tests `--- PASS`, 0 FAIL, among them `TestPreviewWithoutStart`, `TestCheckPositionsBadValues`, `TestBadLayerEditLeavesRunningTreeUnchanged`, `TestRejectedEditWritesNothing`; `boot/config` ok; H-21 stays open. | `manual` |
| 6.13 | a bad layer edit: the running tree is unchanged | composed test: `TestBadLayerEditLeavesRunningTreeUnchanged`, `TestReloadAfterReadyAppliesValidAndRejectsInvalidLayers` in `./internal/boot/...`; record the rejection's file, line and column and the unchanged start count | Windows, WSL | lead session (Claude Code), at the owner's instruction of 2026-11-23; not watched by the owner | `593ceaa3efd55d41bf0eeaced5ba7e26acc71966` | PASS on both lanes. Windows (`register-windows-593ceaa-20261123T2213Z.log`) and WSL (`register-wsl-593ceaa-20261123T2237Z.log`): `--- PASS` for `TestReloadAfterReadyAppliesValidAndRejectsInvalidLayers` (`boot`) and `TestBadLayerEditLeavesRunningTreeUnchanged` (`boot/config`), every `./internal/boot/...` package ok; no file:line:col or start count was printed under `-v`; the assertions of both passed. | `manual` |
| 6.14 | a bad settings edit changes nothing | composed test: `TestRefusedEditsChangeNothing`, `TestDescribeShowsEffectiveValueAfterRejection` in `./internal/storage/settings/` | Windows, WSL | lead session (Claude Code), at the owner's instruction of 2026-11-23; not watched by the owner | `593ceaa3efd55d41bf0eeaced5ba7e26acc71966` | PASS on both lanes. Windows (`register-windows-593ceaa-20261123T2213Z.log`) and WSL (`register-wsl-593ceaa-20261123T2237Z.log`): `--- PASS` for `TestDescribeShowsEffectiveValueAfterRejection`, `TestRefusedEditsChangeNothing`. | `manual` |
| 6.15 | rule-3 default: nothing leaves by default; one off-switch per kind | composed test: `./internal/host/sharing/` | one lane | lead session (Claude Code), at the owner's instruction of 2026-11-23; not watched by the owner | `593ceaa3efd55d41bf0eeaced5ba7e26acc71966` | PASS. Windows (`register-windows-593ceaa-20261123T2213Z.log`): `--- PASS` for `TestEveryKindOffByDefault`, `TestOffSwitchOverridesPermits`, `TestSwitchWithdrawsLivePermit`, `TestConfigureWithdrawsChangedKindsOnly`, `TestWithdrawnInFlightKeepsEgressReason`, `TestPermitsAreReleasedByHolderAndOwner`, `TestEverySharingClientBelongsToAListedSender`. | `manual` |
| 6.16 | rule-3 default: every outbound connection follows one policy | composed test: `TestNoPermitNoDial`, `TestWithdrawnPermitCancelsAndRefuses` in `./internal/util/egress/` | one lane | lead session (Claude Code), at the owner's instruction of 2026-11-23; not watched by the owner | `593ceaa3efd55d41bf0eeaced5ba7e26acc71966` | PASS. Windows (`register-windows-593ceaa-20261123T2213Z.log`): `--- PASS` for `TestNoPermitNoDial`, `TestWithdrawnPermitCancelsAndRefuses`. | `manual` |
| 6.17 | rule-3 default: the test kit runs real components against scripted boundaries | composed test: `TestVirtualClockDrivesDeadlinesWithoutSleeping`, `TestFakeModelScriptsATurn`, `TestLaunchedProgramAndHomeTornDownByCleanup` in `./internal/testkit/...` | one lane | lead session (Claude Code), at the owner's instruction of 2026-11-23; not watched by the owner | `593ceaa3efd55d41bf0eeaced5ba7e26acc71966` | PASS. Windows (`register-windows-593ceaa-20261123T2213Z.log`): `--- PASS` for `TestVirtualClockDrivesDeadlinesWithoutSleeping`, `TestFakeModelScriptsATurn`, `TestLaunchedProgramAndHomeTornDownByCleanup`; every `./internal/testkit/...` package ok. | `manual` |

#### Superseded register values (historical)

Rows filled by gap-closure 14.6.12 at commit `2ee09dc543caa4776e4d886e219e2e72b448af56` (HEAD; a clean `git archive` export), on Windows 11 Enterprise 10.0.28000, go1.27.4, medium integrity (not elevated), and on WSL Ubuntu 24.04.5, kernel 6.18.58.1-microsoft-standard-WSL2, ext4, go1.27.4, non-root, `systemd=true`. They replace the
superseded record at `e77d26b` (see "Gap closure (14.6)"). Each row ran on both lanes,
also where one lane is enough. The Operator column stays blank: an agent ran these, not
a named person (see the runbook). Plan 18.1.5 runs them again at the frozen M1 closure
commit.

The register held these Operator, Commit and Seen values before plan 18.1.5. They are kept as history, labelled `manual` and not closure evidence, and they do not fill a row (the runbook: nothing is carried over from an older commit).

| Step | Operator (then) | Commit (then) | Seen (then) |
| --- | --- | --- | --- |
| 6.1 | -- | `2ee09dc543caa4776e4d886e219e2e72b448af56` | Windows and WSL: 14 tests of `herr`, `ident`, `homedir` and `mediatype` pass; agent run, not an operator check |
| 6.2 | -- | `2ee09dc543caa4776e4d886e219e2e72b448af56` | Windows: `owner job-object`. WSL login shell (cgroup `/non-systemd`): `owner process-group (fallback: own control group not writable; delegated scope not requested (U-K1-1))`. WSL in a delegated scope: `owner control-group`. `TestTreeOwnerIsReported` passes in all three; agent run, not an operator check |
| 6.3 | -- | `2ee09dc543caa4776e4d886e219e2e72b448af56` | Windows: `stopped in 2.0046121s`; WSL login shell: `stopped in 2.010887262s`; WSL delegated scope: `stopped in 2.011134296s`. `TestContextEndStopsDescendantsAfterChildExit` passes on Windows and in the WSL login shell, but fails intermittently in the delegated scope (once in the record run, 2 in 40 on a re-run, also at `e77d26b`: M1-G-BOOT-36), so the control-group row is **not a pass**. After the runs no `subprocess.test` process is alive and no `run-*` scope is left. Without `systemd=true`: not run (see "Gap closure (14.6)"); agent run, not an operator check |
| 6.4 | -- | `2ee09dc543caa4776e4d886e219e2e72b448af56` | Windows and WSL: 10 tests of `boot/launch` pass, `TestLaunchFactsFeedExpressionScope` among them; agent run, not an operator check |
| 6.5 | -- | `2ee09dc543caa4776e4d886e219e2e72b448af56` | Windows: 16 pass and `TestCheckPrivateRefusesASymlink` skips (making a symlink needs a privilege this unelevated user lacks); WSL: 17 pass as user `dsh` (non-root); agent run, not an operator check |
| 6.6 | -- | `2ee09dc543caa4776e4d886e219e2e72b448af56` | Windows and WSL: 16 tests of `human/secret` and `human/secret/redact` pass, `TestSecretResolvedPerUseAndRedacted` and `TestNamedSecretsReachNoChildAndNoLog` among them; agent run, not an operator check |
| 6.7 | -- | `2ee09dc543caa4776e4d886e219e2e72b448af56` | Windows and WSL: 5 tests of `boot/profile` pass, `TestLayerStackPrecedence` among them; agent run, not an operator check |
| 6.8 | -- | `2ee09dc543caa4776e4d886e219e2e72b448af56` | Windows and WSL: 17 tests of `cli` and 7 of `cli/cmdline` pass. By hand, with `dsh` built from the export: `dsh --help` exits 0 with 34 lines on stdout, starting `Usage: dsh [options] [profile\|command...]`, and nothing on stderr. `dsh --no-such-flag` exits **64**, with nothing on stdout and stderr `dsh: cli/usage: unknown flag: --no-such-flag` then `Run 'dsh --help' for usage.`; agent run, not an operator check |
| 6.9 | -- | `2ee09dc543caa4776e4d886e219e2e72b448af56` | Windows and WSL: 9 tests of `cli/output` pass, `TestStatusTableMatchesTheAreaSpec` among them; agent run, not an operator check |
| 6.10 | -- | `2ee09dc543caa4776e4d886e219e2e72b448af56` | Windows and WSL: `TestFatalPrintsOneMessageAndWritesOneReport` passes; it asserts one fatal message on stderr and exactly one file under `reports/`, named by the message; agent run, not an operator check |
| 6.11 | -- | `2ee09dc543caa4776e4d886e219e2e72b448af56` | Windows and WSL: `TestBrokenRequiredEntryFailsBootOnce` passes; it asserts status 78, one fatal message naming the entry and the report, and one report holding `probe (x.probe) at <layer>:2:`, `[required]` and `[boot/required]`; agent run, not an operator check |
| 6.12 | -- | `2ee09dc543caa4776e4d886e219e2e72b448af56` | Windows and WSL: 30 tests of `boot/config` pass, `TestPreviewWithoutStart` among them; H-21 stays open; agent run, not an operator check |
| 6.13 | -- | `2ee09dc543caa4776e4d886e219e2e72b448af56` | Windows and WSL: `TestBadLayerEditLeavesRunningTreeUnchanged` and `TestReloadAfterReadyAppliesValidAndRejectsInvalidLayers` pass. The first asserts the diagnostic at the layer's file, line and column; the effective config and the start count unchanged; and listeners told once; agent run, not an operator check |
| 6.14 | -- | `2ee09dc543caa4776e4d886e219e2e72b448af56` | Windows and WSL: `TestRefusedEditsChangeNothing` and `TestDescribeShowsEffectiveValueAfterRejection` pass; agent run, not an operator check |
| 6.15 | -- | `2ee09dc543caa4776e4d886e219e2e72b448af56` | Windows and WSL: 7 tests of `host/sharing` pass, `TestEverySharingClientBelongsToAListedSender` among them; agent run, not an operator check |
| 6.16 | -- | `2ee09dc543caa4776e4d886e219e2e72b448af56` | Windows and WSL: `TestNoPermitNoDial` and `TestWithdrawnPermitCancelsAndRefuses` pass; agent run, not an operator check |
| 6.17 | -- | `2ee09dc543caa4776e4d886e219e2e72b448af56` | Windows and WSL: `TestVirtualClockDrivesDeadlinesWithoutSleeping`, `TestFakeModelScriptsATurn` and `TestLaunchedProgramAndHomeTornDownByCleanup` pass; agent run, not an operator check |

### Cleanup evidence

**At the closure commit (plan 18.1.7).** Every throwaway worktree of the manual checks was removed, and `git status --porcelain` of the main tree was empty before and after. The signed-in runs left no `claude` or `copilot` process (PID sets equal before and after). The WSL clone's tags (7, all created 2026-11-17) were unchanged, and nothing was published.

At closure: no pid of a stopped tree is alive (a process listing after rows 6.2 and 6.3)
and every temporary home is gone; `git status --porcelain` empty after the run.
Historical: the "no pid alive" observations in the manual-check table above.

### Open items

**At the closure commit (plan 18.1.7).** For this stage: H-24 to H-32 ("Held open" above), whose tests are deferred to DH-S-12, -13 and -16 by M1-D-CLOSE-5. The M1-wide list is [DH-S-11 "Open items at M1"](DH-S-11.md#open-items-at-m1-plan-18111).

At closure: plan 18.1.11 lists them. Now open: H-21 ("Held open" above, closing step
impl.md 16.10); H-24 to H-32 ("Held open" above, closing in gap-closure 19.3 to 19.5 and
impl.md 16.7); the DH-S-6 M1 blockers of plan Stage 14.6 until its change merges and
its tests are tagged (14.6.10); the "second sender for one kind is refused" clause of
`DH-T-HOST-19`, which no test proves and no plan step builds (the sender table is fixed,
with no registration that could refuse a second sender); the 18 DH-T IDs no test names
(M1-G-BOOT-18 to -35, "Gap closure (14.6)"); the intermittent control-group failure of
6.3 (M1-G-BOOT-36); the WSL record without `systemd=true`; the scenarios authored here, which run in DH-S-18, DH-S-21, DH-S-22
and DH-S-39.

**Phase 14 gate runs (Rule 2).** Both lanes pass all 12 gates at `08233be`: run records `wsl-08233be-20261123T0215Z` and `win-08233be-20261123T0223Z` (`manual`; details in [DH-S-1.md](DH-S-1.md), "Phase 14 exit gate runs").
