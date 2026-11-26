@story-harness:DSH-M2 @phase-dh-s-13-sandbox-launch-linux-backends-and-shell-tools @stage-dh-s-13-scenarios-and-built-record @setup-inline
Feature: DH-S-13 scenarios and built record
  DH-S-13 authors nine real-environment scenarios as //go:build scenario tests
  under src/harness/internal/testkit/scenarios/: DH-E-SANDBOX-1,
  DH-E-NATIVESYSTEM-1 to -4 and DH-E-CONTEXT-2 (executed in DH-S-18) and
  DH-E-SANDBOX-2, DH-E-EXEC-7 and DH-E-EXEC-8 (executed in DH-S-39), each indexed
  once in test/harness/acceptance-index.yaml under the stage that runs it
  (M2-DOD-5). Its record docs/stories/harness-DSH-M2/evidence/DH-S-13.md lists the
  fake-backend tests of every security control with their results and the gate
  run of both local lanes (impl.md DH-S-13 exit evidence).

  # M2-DOD-5; proof: service:go-toolchain (the real go toolchain and the gate's
  # own acceptance package, run against this checkout)
  Scenario: Authored S13 scenarios compile and are indexed
    Given the nine DH-S-13 scenario files in "src/harness/internal/testkit/scenarios" and "test/harness/acceptance-index.yaml"
      | id                  | file                                 | stage   | requires                |
      | DH-E-SANDBOX-1      | sandbox_status_scenario_test.go      | DH-S-18 | DH-S-13,DH-S-16         |
      | DH-E-NATIVESYSTEM-1 | sandbox_native_scenario_test.go      | DH-S-18 | DH-S-13,DH-S-16         |
      | DH-E-NATIVESYSTEM-2 | sandbox_native_scenario_test.go      | DH-S-18 | DH-S-13,DH-S-16         |
      | DH-E-NATIVESYSTEM-3 | sandbox_native_scenario_test.go      | DH-S-18 | DH-S-13,DH-S-16         |
      | DH-E-NATIVESYSTEM-4 | sandbox_native_scenario_test.go      | DH-S-18 | DH-S-13,DH-S-16         |
      | DH-E-CONTEXT-2      | context_instructions_scenario_test.go | DH-S-18 | DH-S-13,DH-S-16         |
      | DH-E-SANDBOX-2      | sandbox_unavailable_scenario_test.go | DH-S-39 | DH-S-13,DH-S-16         |
      | DH-E-EXEC-7         | exec_windows_shells_scenario_test.go | DH-S-39 |                         |
      | DH-E-EXEC-8         | exec_confined_scenario_test.go       | DH-S-39 | DH-S-13,DH-S-16,DH-S-26 |
    When "go -C src/harness vet -tags scenario ./internal/testkit/scenarios/..." runs
    And the gate's scenario-index check runs on the checkout
    Then vet exits 0
    And the gate's scenario-index check passes
    And each ID is bound by exactly one scenario test in its named file, built only under the "scenario" tag
    And each ID appears exactly once in the index, under its executing stage, authored "DH-S-13" with its requires
    And the default untagged test run lists none of the nine scenario tests


  # impl.md DH-S-13 exit evidence; proof: service:go-toolchain.
  # The merged commit is the commit "[impl] DH-S-13 Scenarios and Built Record" on
  # this checkout's history; the lanes' commit is DSH_E2E_DHS13_COMMIT when set (a
  # later commit holding the merged one), else the merged commit.
  # The control list below is pinned here, not read from the record: every test
  # runs live on both lanes -- this Windows host, in a temporary worktree of the
  # lanes' commit, and WSL, in an ext4 checkout of it (DSH_E2E_DHS13_WSL_DISTRO,
  # _WSL_USER, _WSL_GO select it) -- in the build(s) it names (default, -tags
  # testhook, or both). Each lane's record column must be what its live run shows
  # (passed = PASS; skipped = SKIP; not run = not built on that OS), and a skip or
  # a not-run counts only because the same test PASSes live on the other lane.
  # A host that reaches no second lane fails the scenario.
  # harness-gate runs live on both lanes from those checkouts, at once, and must
  # run its whole gate list to "PASSED all N gates" on each; a run cut short by
  # the budget fails. The budget is DSH_E2E_DHS13_GATE_BUDGET when set, else what
  # the go test deadline leaves after a 5-minute reserve (never past it). A full
  # run takes about 10 min on WSL and about an hour on Windows, so a green run
  # needs go test -timeout of about 75m or more. Each lane script's own
  # --evidence record of that commit is checked byte for byte as well.
  Scenario: S13 gate green on both lanes
    Given the merged commit of stage DH-S-13 and its record "docs/stories/harness-DSH-M2/evidence/DH-S-13.md"
    And the security controls of steps 13.2 to 13.7 and their fake-backend tests
      | control | test | build |
      | bwrap argv from the Policy | TestBwrapArgvFromPolicy | default |
      | Landlock rules from the Policy | TestLandlockRulesFromPolicy | default |
      | Landlock confines for real | TestLandlockStageConfinesForReal | default |
      | harness binary and home unwritable | TestPrepareKeepsTheHarnessUnwritable | default |
      | symlinked ancestors resolved | TestPrepareSeesThroughSymlinkedAncestors | default |
      | bwrap then Landlock; both failing refuses | TestSelectionOrder | default |
      | unknown bus transport counts as reachable | TestBusProbeCountsTheUnknownReachable | default |
      | session bus observed | TestSessionBusObservedForReal | default |
      | bwrap stage claims only what it sees | TestBwrapStageClaimsOnlyWhatItSees | default |
      | secrets never reach the child | TestSecretsNeverReachTheChild | default |
      | env builder fails closed | TestBuilderFailsClosed | default |
      | refused plan is an error, no exit code | TestRefusedPlanIsAnError | default |
      | command that cannot start is Failed | TestFailedOutcome | default |
      | unknown tool argument rejected | TestUnknownArgumentRejected | default |
      | result carries the confinement | TestToolResultCarriesConfinement | default |
      | no command before go | TestNoCommandBeforeGo | both |
      | host refuses a written canary | TestHostRefusesAWrittenCanary | both |
      | a forced written canary runs nothing | TestForcedWrittenCanaryRunsNothing | testhook |
      | the hook is inert without the tag | TestCanaryHookIsInertWithoutTheTag | default |
      | rejected confirm leaves no command | TestRejectedConfirmLeavesNoCommand | both |
      | late confirm leaves no command | TestLateConfirmLeavesNoCommand | both |
      | confirmed plan runs the command | TestConfirmedPlanRunsTheCommand | both |
      | incomplete plan refused | TestNewLaunchRefusesAnIncompletePlan | both |
      | ready must agree with its plan | TestPlanRefusesDisagreement | both |
      | ready must agree with its plan | TestReadyAgreesWithItsPlan | both |
      | malformed ready frames refused | TestDecodeReadyRefusesMalformedFrames | both |
      | malformed ready frames refused | TestEncodeReadyRefusesInvalidReady | both |
      | stage end or death before ready | TestStageEndsBeforeReady | both |
      | stage end or death before ready | TestStageDeathBeforeReadyNotifiesFailed | both |
      | launch limit on ready | TestReadReadyHonoursTheLaunchLimit | both |
      | exec failure reaches the host | TestExecFailureReachesTheHost | both |
      | canary attempt; unconfined proof tries nothing | TestTryCanary | both |
      | canary attempt; unconfined proof tries nothing | TestProveUnconfinedTriesNothing | both |
      | unconfined plan reaches go | TestUnconfinedPlanReachesGo | both |
      | four-step host decision | TestFourStepHostDecision | default |
      | failed confinement runs nothing | TestFailedConfinementRunsNothing | default |
      | canary below a write root refused | TestCanaryBelowAWriteRootIsRefused | default |
      | weaker evidence wins (seam, confirm) | TestWeakerEvidenceWins | default |
      | weaker evidence wins (seam, confirm) | TestWeakerEvidenceWinsAtConfirm | default |
      | enforcement never stronger than evidence | TestEnforcementIsNeverStrongerThanTheEvidence | default |
      | unconfined only for danger-full-access | TestUnconfinedOnlyForDangerFullAccess | default |
      | config may only remove backends | TestConfigMayOnlyRemoveBackends | default |
      | Windows refuses a confined start | TestWindowsRefusesConfinedStart | default |
      | stage failure before ready reprobes once | TestStageFailureBeforeReadyReprobesOnce | default |
      | empty launch refused | TestStartRefusesAnEmptyLaunch | default |
      | process start needs a plan | TestSpecWithoutAPlanIsRefused | default |
      | process start needs a plan | TestStagedPlanNeedsAValidConfinement | default |
      | staged sequence; rejected confirm stops the stage | TestStagedPlanSequence | default |
      | staged sequence; rejected confirm stops the stage | TestRejectedConfirmStopsTheStage | default |
      | unstaged plan reports unconfined | TestUnstagedPlanReportsUnconfined | default |
      | child environment holds no secret | TestChildEnvironmentHoldsNoSecret | default |
      | child environment holds no secret | TestChildNeverInheritsANamedSecret | default |
      | batch files: rules; by bare name | TestBatchRules | default |
      | batch files: rules; by bare name | TestBatchToolByBareNameWithCheckedArguments | default |
    When the pinned security-control tests run live on both lanes in their builds
    And harness-gate runs live on both lanes at the lanes' commit within its budget
    And the lane records harness-gate wrote with "win.ps1" and "wsl.sh" for the lanes' commit are read
    Then every pinned test is listed in a fake-backend control table of the record with a result on both lanes and none failed
    And the record lists every security-control test of its commands in a fake-backend table
    And every pinned test's live outcome on each lane matches the record's result for that lane
    And every pinned test passed live on at least one lane in every build it names
    And on each lane the live harness-gate run ran every gate of its list and every gate passed
    And the "win" lane record is an intact clean-tree evidence record of that commit on "windows"
    And the "wsl" lane record is an intact clean-tree evidence record of that commit on "linux"
    And every gate of each lane record passes and the record exits 0
    And the record names both lane results with the commit SHA
    And "harness-gate records" accepts the shape of every DH-S-13 record of the story
