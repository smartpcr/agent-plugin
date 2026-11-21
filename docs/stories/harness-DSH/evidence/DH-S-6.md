# DH-S-6 evidence

Stages 6.1 to 6.6 (impl.md steps 6.1 to 6.16) were built by the earlier workstreams
of this phase, merged to `main` up to `f394c24`. Stage 6.7 (step 6.17 and the circle
closure) carries the closure record for DH-S-6. Every result below was re-run on one
tree: `f394c24` plus this stage's change, the commit that carries this record. All
seventeen deliverables are built at that commit.

**Closure status: pending.** Every closure item is recorded below except one: the
manual check "a child that ignores termination ... on each operating system"
(impl.md DH-S-6) is recorded on Windows 11 only. DH-S-6 closes when the Linux and
macOS rows of that check are recorded here (or when the operator changes the
requirement); until then the plan's closure step stays unticked.

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
all pass; `gofmt -l src/harness` is clean.

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

Recorded on Windows 11 (this runner). The Linux and macOS rows are **not recorded**,
and DH-S-6 is not closed without them. They are local manual checks of this stage,
not the DH-E scenario runs that DH-S-18 owns. Why they could not be recorded here:
- No Linux or macOS host was available: WSL has no distribution and docker is absent.
- The CI `harness` lanes on `ubuntu-latest` and `macos-latest` do not reach the
  subprocess tests on `main`. Run 44853707426 at `f394c24` stops at gate 1/8
  (gofmt, `host/sharing/component.go`, fixed by this stage).
To record them, run `go -C src/harness test -count=1 -v -run
'TestStopTreeStopsAndAwaitsTheTree|TestContextEndStopsDescendantsAfterChildExit|TestTreeOwnerIsReported'
./internal/world/subprocess/` on each system and paste the owner line and stop time
into the table.

| Check | Windows 11 | Linux | macOS |
| --- | --- | --- | --- |
| Boot a tree with a broken required entry: one fatal message, one report file | Recorded. `TestBrokenRequiredEntryFailsBootOnce` (one message naming the entry, exit 78, one report under `reports/`), `TestFatalPrintsOneMessageAndWritesOneReport`. | not recorded | not recorded |
| A child that ignores termination: the tree is gone within the bound | Recorded. `TestStopTreeStopsAndAwaitsTheTree`: `owner job-object; stopped in 2.007s` (grace 2 s, then the job is terminated); `TestContextEndStopsDescendantsAfterChildExit`; the kit's launcher test leaves no pid alive. | not recorded (cgroup and process-group paths) | not recorded |
| Edit a layer file badly: the running tree is unchanged | Recorded. `TestBadLayerEditLeavesRunningTreeUnchanged` (the rejection names file, line and column; the tree's config and start count are unchanged); `TestReloadAfterReadyAppliesValidAndRejectsInvalidLayers`. | not recorded | not recorded |

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

Termination in `DH-E-BOOT-1` and `DH-E-HOST-4` is the platform's own signal
(`terminate_unix_test.go`, `terminate_windows_test.go`): SIGTERM on POSIX; on
Windows the host gets a hidden console of its own and the console is closed, which
the Go runtime delivers as `SIGTERM` (`CTRL_CLOSE_EVENT`), since Windows cannot send
SIGTERM to another process. The mechanism was checked on this Windows 11 runner
against a probe program that exits 0 on `SIGTERM` and 130 on an interrupt: it
received `terminated` and exited 0. It was also checked against the `dsh web`
built from this checkout: the host was still running after 5 s, and closing its
console made it exit 0.

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

### Linux ownership fallback (TC-X2, U-K1-1)

On Linux, `world/subprocess` owns a tree through a child control group when the
host's own cgroup v2 is writable. Otherwise it falls back to the process group and
reports it: `Owner{Kind: ProcessGroup, Exact: false, Fallback: "own control group
not writable; delegated scope not requested (U-K1-1)[: cause]"}`
(`owner_linux.go`), which the bash tool's result shows as `owner: process-group`
with one warning. `TestTreeOwnerIsReported` checks the reported owner on each system
(`job-object` here). The Linux run of the fallback is `DH-E-EXEC-1` step 3, in DH-S-18.

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
