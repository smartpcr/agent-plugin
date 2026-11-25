@story-harness:DSH-M2 @phase-dh-s-12-working-directory-filesystem-seam-and-policy-resolver @stage-dh-s-12-scenarios-and-built-record @setup-inline
Feature: DH-S-12 scenarios and built record
  DH-S-12 authors five real-environment scenarios (DH-E-FS-1, DH-E-FS-2,
  DH-E-FS-3, DH-E-VIEW-5, DH-E-SANDBOX-7) as //go:build scenario tests under
  src/harness/internal/testkit/scenarios/, each indexed once in
  test/harness/acceptance-index.yaml under the stage that runs it (M2-DOD-5),
  and records the gate run of both local lanes on the stage's merged commit in
  docs/stories/harness-DSH-M2/evidence/DH-S-12.md (M2-DOD-1).

  # M2-DOD-5; proof: service:go-toolchain (the real go toolchain and the gate's
  # own acceptance package, run against this checkout)
  Scenario: New scenarios compile and are indexed
    Given the five DH-S-12 scenario files in "src/harness/internal/testkit/scenarios" and "test/harness/acceptance-index.yaml"
      | id             | file                                 | stage   | requires                |
      | DH-E-FS-1      | fs_fence_scenario_test.go            | DH-S-18 | DH-S-16                 |
      | DH-E-FS-2      | fs_long_scenario_test.go             | DH-S-18 | DH-S-11,DH-S-16         |
      | DH-E-FS-3      | fs_two_process_scenario_test.go      | DH-S-16 | DH-S-16                 |
      | DH-E-VIEW-5    | view_workdir_scenario_test.go        | DH-S-18 | DH-S-11,DH-S-13,DH-S-16 |
      | DH-E-SANDBOX-7 | sandbox_policy_text_scenario_test.go | DH-S-17 | DH-S-11,DH-S-16         |
    When "go -C src/harness vet -tags scenario ./internal/testkit/scenarios/..." runs
    And the gate's scenario-index check runs on the checkout
    Then vet exits 0
    And the gate's scenario-index check passes
    And each ID is bound by exactly one scenario test in its named file, built only under the "scenario" tag
    And each ID appears exactly once in the index, under its executing stage, authored "DH-S-12" with its requires
    And the default untagged test run lists none of the five scenario tests

  # M2-DOD-1; proof: service:go-toolchain (the lanes' own --evidence records of
  # the merged commit, checked byte for byte, and the gate's records check run live)
  # The merged commit is DSH_E2E_DHS12_COMMIT when set, else the commit
  # "[impl] DH-S-12 Scenarios and Built Record" on this checkout's history.
  Scenario: S12 gate green on both lanes
    Given the merged commit of stage DH-S-12
    When harness-gate runs on "win.ps1" and "wsl.sh" for that commit
    Then the "win" lane record is an intact clean-tree evidence record of that commit on "windows"
    And the "wsl" lane record is an intact clean-tree evidence record of that commit on "linux"
    And every gate of each lane record passes and the record exits 0
    And "docs/stories/harness-DSH-M2/evidence/DH-S-12.md" records both lane results with the commit SHA
    And "harness-gate records" accepts the shape of every DH-S-12 record of the story
