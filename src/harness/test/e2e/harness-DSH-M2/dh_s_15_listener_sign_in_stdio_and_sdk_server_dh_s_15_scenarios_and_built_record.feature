@story-harness:DSH-M2 @phase-dh-s-15-listener-sign-in-stdio-and-sdk-server @stage-dh-s-15-scenarios-and-built-record @setup-inline
Feature: DH-S-15 scenarios and built record
  DH-S-15 (Stage 5.9) authors five HOST scenarios (DH-E-HOST-1, DH-E-HOST-2,
  DH-E-HOST-7, DH-E-HOST-8, DH-E-HOST-9) as //go:build scenario tests under
  src/harness/internal/testkit/scenarios/, compile only here, each indexed once in
  test/harness/acceptance-index.yaml under the stage that runs it, on the M1
  scenario helpers and raw JSON-RPC, never on the Go client src/sdk/sdk (M2-DOD-5).
  Its merged commit passes every gate on both local lanes with src/sdk/protocol
  unchanged (M2-DOD-1, TC-12g), as docs/stories/harness-DSH-M2/evidence/DH-S-15.md
  records.

  # M2-DOD-5; proof: service:go-toolchain (the real go toolchain and the gate's
  # own acceptance package, run against this checkout)
  Scenario: Authored S15 scenarios compile and are indexed
    Given the five DH-S-15 HOST files in "src/harness/internal/testkit/scenarios" and "test/harness/acceptance-index.yaml"
      | id          | file                                   | stage   | requires                        |
      | DH-E-HOST-1 | host_signin_scenario_test.go           | DH-S-22 | DH-S-16,DH-S-21                 |
      | DH-E-HOST-2 | host_network_scenario_test.go          | DH-S-22 | DH-S-16,DH-S-21                 |
      | DH-E-HOST-7 | host_takeover_linux_scenario_test.go   | DH-S-39 | DH-S-13,DH-S-16,DH-S-21         |
      | DH-E-HOST-8 | host_takeover_macos_scenario_test.go   | DH-S-39 | DH-S-13,DH-S-16,DH-S-21,DH-S-27 |
      | DH-E-HOST-9 | host_takeover_windows_scenario_test.go | DH-S-39 | DH-S-13,DH-S-16,DH-S-21,DH-S-27 |
    When "go -C src/harness vet -tags scenario ./internal/testkit/scenarios/..." runs for "windows", "linux" and "darwin"
    And the gate's scenario-index check runs on the checkout
    Then vet exits 0 for each
    And the gate's scenario-index check passes
    And each ID is bound by exactly one scenario test in its named file, built only under the "scenario" tag
    And each ID appears exactly once in the index, under its executing stage, authored "DH-S-15" with its requires
    And the default untagged test run lists none of the five scenario tests
    And none of the five files imports "github.com/smartpcr/agent-plugin/src/sdk/sdk"
    And the scenario package under the "scenario" tag does not depend on "github.com/smartpcr/agent-plugin/src/sdk/sdk" for each

  # M2-DOD-1, TC-12g; proof: service:go-toolchain (the lanes' own --evidence
  # records of the merged commit, committed under the M1 runs directory and checked
  # byte for byte; the lanes run outside the test, by the Full harness gate policy)
  # The merged commit is DSH_E2E_DHS15_COMMIT when set, else the commit
  # "[impl] DH-S-15 Scenarios and Built Record" on this checkout's history.
  Scenario: S15 gate green on both lanes
    Given the merged commit of stage DH-S-15
    Then the merged commit leaves "src/sdk/protocol" unchanged
    And the check is live: the latest commit that changed "src/sdk/protocol" is refused
    When harness-gate runs on "win.ps1" and "wsl.sh" for that commit
    Then the "win" lane record is an intact clean-tree evidence record of that commit on "windows"
    And the "wsl" lane record is an intact clean-tree evidence record of that commit on "linux"
    And every gate of each lane record passes and the record exits 0
    And the "scenario-index" gate is among the passing gates of each lane record
    And "docs/stories/harness-DSH-M2/evidence/DH-S-15.md" records both lane results with the commit SHA
