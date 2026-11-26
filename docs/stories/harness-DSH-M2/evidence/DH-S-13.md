# DH-S-13 evidence

Closure status: open

Evidence record of [impl.md DH-S-13](../../../harness/impl.md#dh-s-13) (Sandbox launch, Linux backends and shell tools) for story
`harness:DSH-M2`, in the impl.md sec. 7.3 form of the M1 records
([../../harness-DSH-M1/evidence/README.md](../../harness-DSH-M1/evidence/README.md)). Plan:
[implementation-plan.md](../implementation-plan.md) Phase 3. Decisions of
[../../harness-DSH-M1/decisions.md](../../harness-DSH-M1/decisions.md) stay in force.

**Kind.** Build, P0. Paired acceptance stage: DH-S-18.

**How this record closes** (C57; M2-C-2; [the gate's README](../../../../tools/harness-gate/README.md#stage-closure)):

1. While the line above reads `open`, fill the tables below until `harness-gate ready --stage DH-S-13 --built`
   exits 0. `ready` does not read the status line.
2. Only then does the closing PR change the line to `Closure status: closed`.
3. That PR's gate runs `harness-gate close --stage DH-S-13 --built`: every `ready` condition again, and the
   line reading `closed`. A `closed` line on a record that is not ready is refused `closed-but-not-ready`.

No M2 scenario run and no M2 stage closure happens before M1 is closed (M2-DEC-1; `harness-gate entry`,
implementation-plan Stage 1.2). The four tables below are read by the gate: keep their headings and columns.
A result is `passed`, `failed`, `not run (<reason>)`, `skipped`, `blocked` or `out of scope (<decision>)`;
skipped, blocked, not run and an empty cell count as open (impl.md sec. 7.3, M2-DEC-3), and only a
variant out of scope by a named decision is not a blocker (M2-C-2). A `recorded` leg must run; its failure
is kept and does not block (M2-DEC-4). A commit is a SHA of 7 to 40 lower-case hex digits.

## Steps

Each step's merge commit; `--built` refuses a step without one.

| Step | Deliverable | Commit | Notes |
| --- | --- | --- | --- |
| 13.1 | DH-D-SANDBOX-2 | 5ba19c4 | |
| 13.2 | DH-D-EXEC-4 | decfd62 | |
| 13.3 | DH-D-SANDBOX-3 | c026bce | |
| 13.4 | DH-D-SANDBOX-4 | fb79bbb | `world/sandbox/local`: bubblewrap backend (`bwrap` resolved once by absolute path; argv from the Policy, golden `local/testdata/bwrap_argv.golden`, `TestBwrapArgvFromPolicy`) and pure-Go Landlock backend through go-landlock v0.11.2 (`launch/landlock.go`, `launch/confine_linux.go`: `NewRulesetFD`, locked thread, `no_new_privs`, `LandlockRestrictSelf`; ABI probe, rules from the Policy, applied by the stage to itself; `TestLandlockRulesFromPolicy`, `TestLandlockStageConfinesForReal`); Prepare refuses a root that would make the harness binary or home writable where the backend cannot overlay it, judging each root, the home and the binary as written and with every symbolic link resolved, and refusing a path it cannot resolve (`local/protect.go`, `TestPrepareKeepsTheHarnessUnwritable`, `TestPrepareSeesThroughSymlinkedAncestors`); order bwrap then Landlock, both failing refuses a confined policy `sandbox/unavailable` (`TestSelectionOrder`). I2 Q4 default applied, see Owner defaults. |
| 13.5 | DH-D-EXEC-3 | ea4e559 | |
| 13.6 | DH-D-EXEC-8 | 07b34c4 | `world/shellexec` (C39): `Resolve` fills the dialect and the default timeout (120 s), cuts a timeout above the cap (600 s) and refuses a blank or NUL command, a relative directory, a negative timeout or no policy (`TestResolveAppliesDefaultsAndCaps`, `TestResolveRefuses`); `Start` builds the dialect's argv, the child environment through `shellexec/env`, asks `sandbox.Service.Prepare` (C37) for the plan and starts through `subprocess.Start` (C38), never `os/exec`; the timeout runs from the confirmed start; one `Execution` serves the foreground `Wait` and background `Reader`s, and ending a foreground wait does not stop the command (`TestForegroundWaitLeavesABackgroundRun`); a command that cannot start (no plan, refused confinement, no environment, no argv) is an explicit `Failed` outcome (`TestFailedOutcome`); `TimedOut` and `Cancelled` carry their first cause (`TestTimeoutStopsTheCommand`); once the shell exits, the wait for its streams is bounded (`Limits.Drain`, 2 s, through the new `subprocess.Spec.Drain`), so a descendant holding stdout (`sleep 30 & echo hi; exit 3`) cannot hold back the outcome: exit code and tail kept, the descendant stopped with the tree (`TestDescendantHoldingStdout`); the private directory (PowerShell script file) is made under a workspace-write policy's scratch root, else its first write root, so a confiner hiding the host `/tmp` (bubblewrap) still binds it, without widening the policy (`TestPrivateDirIsVisibleUnderThePolicy`). `/bash`: configured path, Git for Windows, PATH (the System32 and WindowsApps WSL launcher skipped), `bash -c`, `BASH_ENV`/`ENV` unset (`TestFindOrder`, `TestBashExecutor`: `echo hi; exit 3` gives exit 3, tail `hi`, Unconfined on the Linux lane, failing if bash is missing, TC-06b). `/pwsh`: configured path, PowerShell 7 install, PATH, Windows PowerShell 5.1 when allowed, edition reported; `-NoLogo -NoProfile -NonInteractive -Command` after a UTF-8 preamble; a `param`/`using`/`#requires` first statement, after leading white space, BOM and `#`/`<# #>` comments, runs from a private BOM script file with its exit code carried (`TestFirstStatement`, `TestArgv`, `TestPwshExecutor`: `Write-Output hi; exit 3` gives exit 3, tail `hi` on the Windows lane, TC-06b). `/env`: managed-variable registry (built-ins `DSH_HOME`, `DSH_EXECUTABLE` first; duplicate, reserved `DSH_TOOL_*`, malformed and undeclared keys refused; contributor leaves with its fiber) and the scrubbed builder: every name `human/secret` holds, configured or stored, is dropped from the host, the entries and the managed snapshot, failing closed when the store cannot list them (`TestSecretsNeverReachTheChild` with `DSH_TEST_TOKEN`, `TestBuilderFailsClosed`, M2-C-11). Pwsh 5.1 non-ASCII stdin stays open: see Open items (`UP-DEBT-limit.packages.shell.pwsh-local.7`, DH-E-EXEC-7 at DH-S-39, M2-I-2). |
| 13.7 | DH-D-EXEC-9 | 7d00271 | `world/shellexec/tools` (C40): the `bash` and `pwsh` tools, both always declared (`shellexec.tools` component, injects `shellexec.Key`, `sandbox.ResolverKey`, `workdir.Key`, `tools.Key`); named arguments only, `command` (required), `timeout` (milliseconds, 0 the shell's default, cut to its cap) and `description`, strict decoding refusing any other, `stdin` and `env` included, with `tools/invalid-arguments` naming it before anything starts (`TestUnknownArgumentRejected`, TC-06b logic; `TestArgumentsAreNamedOnly`); per call the session's policy (C33) and working directory are resolved and the command runs through the dialect's executor (C39); a dialect with no executor fails `shellexec/not-found` and a command that never started fails with its reason, no exit code (`TestNoExecutorIsNotFound`, `TestRefusedPlanIsAnError`); the result is structured JSON: `outcome`, `exit_code`, `stop` (timeout or cancelled, budget, cause), `timeout_ms`, `duration_ms`, each stream's `tail` (the executor's ring, `Limits.TailBytes`, 64 000 bytes, the tool cutting nothing the collector kept: bytes that are not UTF-8 are shown as U+FFFD, or as `?` where U+FFFD would not fit, and marked `replaced`, so `truncated` is exactly the collector's and, with a spill store mounted, a truncated tail is one the collector spilled whole; without a store, or when the spill fails, the result has no `spill` or a `spill_error`), `bytes`, `truncated`, `replaced` and `spill` reference, and `confinement` (`mode`, `state`, `backend`, `missing`, `exposed`) (`TestTailIsBoundedWithSpill`; `TestOutputPastTheRingIsSpilled`, collector to result with a spill store: 40 000 bytes under the default ring shown whole, 70 000 truncated to 64 000 with a 70 000-byte spill, 900 and 1 500 bytes under a 1 000-byte ring, `printf '\377a\377'` under a 3-byte ring shown whole as `?a?`; `TestTimeoutIsCutBeforeConversion`: `timeout` values that would overflow `time.Duration` resolve to the cap; `TestToolResultCarriesConfinement`: `true` logs `exit_code` 0 and `confinement.state` `unconfined` under danger-full-access, `echo hi; exit 3` exit 3 tail `hi`, failing if bash is missing on the Linux lane, TC-06b; `TestPwshToolThroughThePipeline`: `Write-Output hi; exit 3` exit 3 tail `hi` on the Windows lane, TC-06b). All tagged DH-T-EXEC-15. Not built here: promotion to a background job on timeout (`run_in_background`, DH-D-JOBS-1 is not mounted in M2) and the `workdir` argument. |
| 13.8 | DH-D-CONTEXT-2 | fbbb14a | |

## Scenarios

None executed here. Authored here: DH-E-NATIVESYSTEM-1 to -4 and DH-E-SANDBOX-1 (run in DH-S-18); DH-E-SANDBOX-2, DH-E-EXEC-7 and DH-E-EXEC-8 (run in DH-S-39).

| Scenario | Variant | Venue | Leg | Environment | Commit | Result | Artifacts |
| --- | --- | --- | --- | --- | --- | --- | --- |

## Manual checks register

The manual checks impl.md DH-S-13 names, each `manual`, on scratch data: what was seen, by whom, at
which commit (impl.md sec. 7.3).

| Step | Check | Seen | Operator | Commit | Label |
| --- | --- | --- | --- | --- | --- |
| 13.4 | on a Linux host, a confined command that writes outside its roots: the denial and the reported enforcement | WSL2 Ubuntu 24.04, kernel 6.18.58.1-microsoft-standard-WSL2, no `bwrap`: production chain `local.Platform()`; status bwrap unavailable ("bwrap not found"), landlock available, canary refused; `sh -c "printf x > <outside>/M"` under workspace-write exited 2, stderr `cannot create .../M: Permission denied`, confinement `partial`, `landlock`, missing [network harness-home delegated-execution]; M absent. Control: the same write beneath the root exited 0 and made M | Bao Er (agent) | 716a084 | `manual` |
| 13.3 | both backends removed: the command did not run | Same host, no `bwrap`; Landlock made unusable to the process by an unprivileged seccomp filter (`systemd-run --user -p NoNewPrivileges=yes -p 'SystemCallFilter=~landlock_create_ruleset landlock_add_rule landlock_restrict_self' -p SystemCallErrorNumber=ENOSYS`), not disabled at boot; status both unavailable (landlock: "the kernel has no Landlock (interface version 0)"); workspace-write `Prepare` refused `sandbox/unavailable` ("...cannot be confined and was not started"); marker M inside the root absent | Bao Er (agent) | 716a084 | `manual` |

## Owner defaults (M2-C-4)

The defaults of [impl.md sec. 9.1](../../../harness/impl.md#91-defaults-the-owner-has-not-confirmed) whose
"Confirm before" is DH-S-13. The owner confirms each, or changes the document that holds it, before this
stage closes; `--built` refuses a row with no confirmation or no name in By.

| Default | Raised in | Text (impl.md sec. 9.1) | Confirmed | By | Commit |
| --- | --- | --- | --- | --- | --- |
| `partial` when delegated execution cannot be stopped | I2 Q4 | A sandbox that cannot stop delegated execution is reported `partial` | | | |

Applied, unconfirmed, in step 13.4. Both Linux confiners (`launch/observe.go` `confineBwrap` and
`launch/confine_linux.go` `confineLandlock`) end in the shared `delegated()`, which claims
`delegated-execution` governed only when the stage's bus observation (`launch/bus.go` `busProbe`, fed by
`launch/bus_linux.go`) shows every session service absent: the address in `DBUS_SESSION_BUS_ADDRESS` and
`bus` and `systemd/private` under `/run/user/<uid>` and `$XDG_RUNTIME_DIR`, each dial failing with "no
such file", "not a directory" or "connection refused". Under bubblewrap the stage looks after the
runtime directory is hidden by `--tmpfs`; under Landlock it looks just before restricting itself, since
this reference set does not scope sockets, so on a desktop session with a live bus Landlock does not
claim it. `delegated-execution` is missing, and `sandbox.Enforce` reports the start `partial` naming it,
whenever a service answers, the address names any other transport (`tcp:`, `nonce-tcp:`, `unixexec:`,
`launchd:`, `systemd:`, `autolaunch:`, an abstract socket, `runtime=yes` with no runtime directory) or is
malformed, or a dial fails for any other reason (a time out, a permission denial); off Linux it is
always missing. Tests: `TestBusProbeCountsTheUnknownReachable`, `TestSessionBusObservedForReal`,
`TestBwrapStageClaimsOnlyWhatItSees`, `TestLandlockRulesFromPolicy`.

Decisions taken from the design in step 13.4 (these are not impl.md sec. 9.1 defaults, so they have no row above).
The operator approved all four as implemented on 2026-11-25 (Forge questions `stage-3.4-bundle`,
`config-home-source`, `fs-write-abi-gate`, `bwrap-private-tmp`, `landlock-shared-tmp`):

- **fs-write version gate.** Landlock reports `fs-write` governed only at the reference version 5;
  below it `fs-write` is in `Missing`. This is DH-ADR-NATIVESYSTEM-2 ("a named reference version (5 at
  first, as upstream)", `full` only "when the kernel's version is at least the reference")
  (`launch/landlock.go`, `TestLandlockRulesFromPolicy`).
- **Private `/tmp` under bubblewrap.** Under `workspace-write`, bubblewrap mounts `--tmpfs /tmp`, which
  goes away when the start ends. Under `read-only` the host `/tmp` stays read-only. This follows
  DH-ADR-SANDBOX-4 (only Seatbelt keeps upstream's shared temp areas) and the bubblewrap
  row of sandbox-ssh.md sec. 3.2. It comes before every bind, so a root, the canary or the binary under
  `/tmp` is still the host's (`local/bwrap.go`, `testdata/bwrap_argv.golden`, `TestBwrapArgvFromPolicy`).
- **No shared `/tmp` under Landlock.** Landlock grants writes only beneath the policy's roots, as TC-04a
  requires, and does not grant the shared host `/tmp`; programs find the scratch through `TMPDIR`. The
  DH-ADR-SANDBOX-4 sentences that said Landlock keeps the shared `/tmp` were amended to match
  (`docs/harness/areas/sandbox-ssh.md`).
- **Where `Config.Home` comes from.** Whoever builds the sandbox service passes the resolved harness
  home. No production code builds `world/sandbox/local` yet; every caller is a test. Until a caller
  exists, an empty `Home` means no start claims `harness-home`, and the inside-home refusal has nothing
  to compare against (`local/local.go` `Config.Home`).

## Evidence record (impl.md sec. 7.3)

### Source and build identity

Not yet recorded. At closure: the commit of the three modules (one commit), the Go version (`go1.27.4`),
the binary's SHA-256 and embedded version.

### Environment and fixture

Not yet recorded. Gate venue: both local lanes, `tools/harness-gate/lanes/win.ps1` (Windows native) and
`lanes/wsl.sh` (Linux under WSL); macOS out of scope (`platforms-windows-wsl`).

### Exact tests and commands

The manual checks were driven by a throwaway `go test` file in `world/sandbox/local` (deleted after the run;
the backends, the service and the start are the product's), built with `go test -c` in WSL and run
directly (13.4) and under the `systemd-run` filter above (13.3).

Security-control tests with fake backends (and the two real-kernel ones), per step 13.4 to 13.7:

```
go -C src/harness test -count=1 -v -run '^(TestBwrapArgvFromPolicy|TestLandlockRulesFromPolicy|TestLandlockStageConfinesForReal|TestPrepareKeepsTheHarnessUnwritable|TestPrepareSeesThroughSymlinkedAncestors|TestSelectionOrder|TestBusProbeCountsTheUnknownReachable|TestSessionBusObservedForReal|TestBwrapStageClaimsOnlyWhatItSees|TestSecretsNeverReachTheChild|TestBuilderFailsClosed|TestRefusedPlanIsAnError|TestFailedOutcome|TestUnknownArgumentRejected|TestToolResultCarriesConfinement)$' ./internal/world/sandbox/... ./internal/world/shellexec/...
```

run on Windows 10.0.28000 (go1.27.4) and in WSL2 Ubuntu 24.04 (same command, from the checkout under
`/mnt/e`), at 716a084 plus the uncommitted iteration-2 diff, which touches none of these packages.

Launch-authorization controls (step 13.2/13.3: plan, stage, ready, canary, confirm, go) and the rest of
the sandbox tree, every test of the packages rather than a selection, plus the forced-canary hook build
and the process package's plan and environment controls:

```
go -C src/harness test -count=1 -v ./internal/world/sandbox/...
go -C src/harness test -count=1 -v -tags testhook ./internal/world/sandbox/launch/
go -C src/harness test -count=1 -v -run '^(TestSpecWithoutAPlanIsRefused|TestStartRefusesAnEmptyLaunch|TestStagedPlanSequence|TestRejectedConfirmStopsTheStage|TestStagedPlanNeedsAValidConfinement|TestUnstagedPlanReportsUnconfined|TestChildEnvironmentHoldsNoSecret|TestChildNeverInheritsANamedSecret|TestBatchRules|TestBatchToolByBareNameWithCheckedArguments)$' ./internal/world/subprocess/
```

run on Windows 10.0.28000 and in WSL2 Ubuntu 24.04 (kernel 6.18.58.1, go1.27.4, from `/mnt/e`) at c3c4a25
plus the uncommitted iteration-3 diff, which touches only two scenario files outside these packages.

Gate: `pwsh -File tools/harness-gate/lanes/win.ps1` from a temporary worktree at c3c4a25
(`E:\work\lanes\dhs13-c3c4a25`, removed after the run, log `E:\work\lanes\win-c3c4a25.log`), and
`tools/harness-gate/lanes/wsl.sh` from an ext4 clone at c3c4a25 (`~/src/dhs13-c3c4a25`, removed after the
run; preflight "ext4 checkout, uid 1000, go1.27.4, CC=gcc, clean tree"; log copied to
`E:\work\lanes\wsl-c3c4a25.log`). An earlier Windows run at 716a084 is under Results.
`DSH_REQUIRE_SCENARIOS` at closure is still to record.

### Run identifiers

Not yet recorded: CI run and job ids, or the local run-record id and its SHA-256 (M1 runbook template);
session ids and provider request ids for model calls.

### Results

The Scenarios table above, and the local test results per step at closure.

Security controls, fake-backend tests (Windows / Linux under WSL):

| Control | Test | Windows | Linux (WSL) |
| --- | --- | --- | --- |
| bwrap argv from the Policy | `TestBwrapArgvFromPolicy` | passed | passed |
| Landlock rules from the Policy | `TestLandlockRulesFromPolicy` | passed | passed |
| Landlock confines for real | `TestLandlockStageConfinesForReal` | not run (linux build tag) | passed |
| harness binary and home unwritable | `TestPrepareKeepsTheHarnessUnwritable` | passed | passed |
| symlinked ancestors resolved | `TestPrepareSeesThroughSymlinkedAncestors` | skipped (no symlink privilege) | passed |
| bwrap then Landlock; both failing refuses | `TestSelectionOrder` | passed | passed |
| unknown bus transport counts as reachable | `TestBusProbeCountsTheUnknownReachable` | passed | passed |
| session bus observed | `TestSessionBusObservedForReal` | not run (linux build tag) | passed |
| bwrap stage claims only what it sees | `TestBwrapStageClaimsOnlyWhatItSees` | passed | passed |
| secrets never reach the child | `TestSecretsNeverReachTheChild` | passed | passed |
| env builder fails closed | `TestBuilderFailsClosed` | passed | passed |
| refused plan is an error, no exit code | `TestRefusedPlanIsAnError` | passed | passed |
| command that cannot start is `Failed` | `TestFailedOutcome` | passed | passed |
| unknown tool argument rejected | `TestUnknownArgumentRejected` | passed | passed |
| result carries the confinement | `TestToolResultCarriesConfinement` | passed | passed |

Launch-authorization controls, fake stage and fake backends (Windows / Linux under WSL; `[testhook]` is the
`-tags testhook` build of the launch package):

| Control | Test | Windows | Linux (WSL) |
| --- | --- | --- | --- |
| no command before go | `TestNoCommandBeforeGo` | passed (both builds) | passed (both builds) |
| host refuses a written canary | `TestHostRefusesAWrittenCanary` | passed (both builds) | passed (both builds) |
| a forced written canary runs nothing | `TestForcedWrittenCanaryRunsNothing[testhook]` | passed | passed |
| the hook is inert without the tag | `TestCanaryHookIsInertWithoutTheTag` | passed | passed |
| rejected confirm leaves no command | `TestRejectedConfirmLeavesNoCommand` | passed (both builds) | passed (both builds) |
| late confirm leaves no command | `TestLateConfirmLeavesNoCommand` | passed (both builds) | passed (both builds) |
| confirmed plan runs the command | `TestConfirmedPlanRunsTheCommand` | passed (both builds) | passed (both builds) |
| incomplete plan refused | `TestNewLaunchRefusesAnIncompletePlan` | passed (both builds) | passed (both builds) |
| ready must agree with its plan | `TestPlanRefusesDisagreement`, `TestReadyAgreesWithItsPlan` | passed (both builds) | passed (both builds) |
| malformed ready frames refused | `TestDecodeReadyRefusesMalformedFrames`, `TestEncodeReadyRefusesInvalidReady` | passed (both builds) | passed (both builds) |
| stage end or death before ready | `TestStageEndsBeforeReady`, `TestStageDeathBeforeReadyNotifiesFailed` | passed (both builds) | passed (both builds) |
| launch limit on ready | `TestReadReadyHonoursTheLaunchLimit` | passed (both builds) | passed (both builds) |
| exec failure reaches the host | `TestExecFailureReachesTheHost` | passed (both builds) | passed (both builds) |
| canary attempt; unconfined proof tries nothing | `TestTryCanary`, `TestProveUnconfinedTriesNothing` | passed (both builds) | passed (both builds) |
| unconfined plan reaches go | `TestUnconfinedPlanReachesGo` | passed (both builds) | passed (both builds) |
| four-step host decision | `TestFourStepHostDecision` | passed | passed |
| failed confinement runs nothing | `TestFailedConfinementRunsNothing` | passed | passed |
| canary below a write root refused | `TestCanaryBelowAWriteRootIsRefused` | passed | passed |
| weaker evidence wins (seam, confirm) | `TestWeakerEvidenceWins`, `TestWeakerEvidenceWinsAtConfirm` | passed | passed |
| enforcement never stronger than evidence | `TestEnforcementIsNeverStrongerThanTheEvidence` | passed | passed |
| unconfined only for danger-full-access | `TestUnconfinedOnlyForDangerFullAccess` | passed | passed |
| config may only remove backends | `TestConfigMayOnlyRemoveBackends` | passed | passed |
| Windows refuses a confined start | `TestWindowsRefusesConfinedStart` | passed | skipped (Windows row, DH-D-SANDBOX-4) |
| stage failure before ready reprobes once | `TestStageFailureBeforeReadyReprobesOnce` | passed | passed |
| empty launch refused | `TestStartRefusesAnEmptyLaunch` | passed | passed |
| process start needs a plan | `TestSpecWithoutAPlanIsRefused`, `TestStagedPlanNeedsAValidConfinement` | passed | passed |
| staged sequence; rejected confirm stops the stage | `TestStagedPlanSequence`, `TestRejectedConfirmStopsTheStage` | passed | passed |
| unstaged plan reports unconfined | `TestUnstagedPlanReportsUnconfined` | passed | passed |
| child environment holds no secret | `TestChildEnvironmentHoldsNoSecret`, `TestChildNeverInheritsANamedSecret` | passed | passed |
| batch files: rules; by bare name | `TestBatchRules`, `TestBatchToolByBareNameWithCheckedArguments` | passed, passed | passed, skipped (Windows only) |

Whole runs: `world/sandbox/...` (sandbox, launch, local, policy, sandboxtest) and the testhook build, `ok` on
every package on both systems; `TestStartRefusesAnEmptyLaunch` passed on both in the process-package run.
Of the distinct top-level tests (with the testhook build counted
separately), Windows 124: 123 passed, 1 skipped (`TestPrepareSeesThroughSymlinkedAncestors`, no symlink
privilege). Linux 127: 123 passed, 4 skipped, all Windows path or Windows-only rows
(`TestEscalateComparesPathsAsTheHostDoes`, `TestRootsCompareAsTheHostDoes`, `TestWindowsRefusesConfinedStart`,
`TestBatchToolByBareNameWithCheckedArguments`). No test failed.

Gate lanes:

- 716a084, Windows: failed at gate 1/13 (gofmt) on the #135 e2e file below, one field misaligned; fixed
  by `gofmt -w` in c3c4a25. Linux was not run.
- c3c4a25, both lanes: gofmt passed; both **failed at gate 2/13 (vet)**, gate exit 1 (Windows vet 13m6.63s,
  ended 2026-11-26T01:31:17Z; WSL vet 15.721s). The same four `nosleep` analyzer findings (QG-4) on both,
  under `-tags=`, `-tags=replay,scenario,bench` and `-tags=e2e`, all in code this stage's diff does not
  touch:
  - `src/harness/internal/world/fs/local/local_test.go:309:77: time.Sleep in a test file: wait on virtual time (testing/synctest) or an explicit signal (QG-4)` (last changed by #146)
  - `src/harness/test/e2e/harness-DSH-M2/dh_s_13_sandbox_launch_linux_backends_and_shell_tools_launch_plan_required_for_every_command_test.go:302:2` (same finding; #131)
  - `src/harness/test/e2e/harness-DSH-M2/dh_s_13_sandbox_launch_linux_backends_and_shell_tools_sandbox_seam_probe_select_confirm_test.go:400:3` and `:494:2` (same finding; #135)
  - Records (copied out of the lane checkouts, which are removed, to `E:\work\lanes\`):
    `win-c3c4a25-20261126T0117Z.txt` (sha256
    `5f9bb8aa0fd55caac1590b55785848e34ddcbca81c888aa39c7602df12522bd1`), `wsl-c3c4a25-20261126T0119Z.txt` and
    `-rehearse.txt` (rehearse exit 0), with the lane logs `win-c3c4a25.log` and `wsl-c3c4a25.log`.
  - Gates 3 to 13 did not run on either lane. The stage stays open until every gate passes on both lanes
    on the final commit.

### Manual checks

The Manual checks register above.

### Cleanup evidence

Not yet recorded: the before-and-after comparison the stage names, or what was retained.

### Open items

Unresolved debt `UP-DEBT-limit.packages.shell.pwsh-local.7` (non-ASCII standard input under Windows PowerShell 5.1) closes with DH-E-EXEC-7 in DH-S-39. Built-not-done until DH-S-39: SANDBOX-2, SANDBOX-3, EXEC-4, EXEC-8, EXEC-9; CONTEXT-2 until DH-S-32 (DH-E-CONTEXT-1).
