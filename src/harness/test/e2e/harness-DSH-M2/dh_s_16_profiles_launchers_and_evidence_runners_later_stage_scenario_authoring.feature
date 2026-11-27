@story-harness:DSH-M2 @phase-dh-s-16-profiles-launchers-and-evidence-runners @stage-later-stage-scenario-authoring @setup-inline
Feature: Later-stage scenario authoring
  DH-S-16 (Stage 6.11) leaves the 17 scenarios of tech-spec sec. 4.5 authored as
  //go:build scenario tests under src/harness/internal/testkit/scenarios/, plus
  DH-E-SDK-1 in sdk_stdio_scenario_test.go. They compile on every lane and each is
  indexed once in test/harness/acceptance-index.yaml under the stage that executes
  it, never under DH-S-16..18 (M2-DOD-5).

  # M2-DOD-5; proof: service:go-toolchain (the real go toolchain cross-compiling
  # the scenario package for each lane's GOOS: windows for win.ps1, linux for wsl.sh)
  Scenario: Later-run scenarios compile
    Given the later-run scenario files in "src/harness/internal/testkit/scenarios"
      | id            | file                                   |
      | DH-E-HOST-1   | host_signin_scenario_test.go           |
      | DH-E-HOST-2   | host_network_scenario_test.go          |
      | DH-E-BUNDLE-3 | bundle_egress_scenario_test.go         |
      | DH-E-CLI-1    | cli_launch_scenario_test.go            |
      | DH-E-CLI-2    | cli_headless_scenario_test.go          |
      | DH-E-SANDBOX-2| sandbox_unavailable_scenario_test.go   |
      | DH-E-EXEC-7   | exec_windows_shells_scenario_test.go   |
      | DH-E-EXEC-8   | exec_confined_scenario_test.go         |
      | DH-E-HOST-7   | host_takeover_linux_scenario_test.go   |
      | DH-E-HOST-8   | host_takeover_macos_scenario_test.go   |
      | DH-E-HOST-9   | host_takeover_windows_scenario_test.go |
      | DH-E-SDK-2    | sdk_client_loss_scenario_test.go       |
      | DH-E-UTIL-2   | scenario_required_scenario_test.go     |
      | DH-E-BUNDLE-1 | bundle_preset_scenario_test.go         |
      | DH-E-BUNDLE-2 | bundle_preset_scenario_test.go         |
      | DH-E-BUNDLE-4 | bundle_release_scenario_test.go        |
      | DH-E-API-5    | api_feature_routes_scenario_test.go    |
      | DH-E-SDK-1    | sdk_stdio_scenario_test.go             |
    Then the table lists the 17 IDs of tech-spec sec. 4.5 in "docs/stories/harness-DSH-M2/tech-spec.md" plus "DH-E-SDK-1"
    And each file carries its "//dsh:scenario" directives and is built only under the "scenario" tag
    When "go -C src/harness vet -tags scenario ./internal/testkit/scenarios/..." runs for "windows" and "linux"
    Then vet exits 0 for each

  # M2-DOD-5; proof: golden (the committed index read by the gate's own
  # acceptance.ReadIndex, projected onto the 17 IDs and compared with
  # testdata/dh_s_16_later_run_index.golden; the golden's ID set is tech-spec sec. 4.5)
  Scenario: Index count
    Given the committed "test/harness/acceptance-index.yaml"
    And the golden "testdata/dh_s_16_later_run_index.golden" of the 17 IDs of tech-spec sec. 4.5 in "docs/stories/harness-DSH-M2/tech-spec.md"
    When the index check counts the 17 IDs
    Then each ID appears exactly once in the index
    And each ID is under the stage the golden states
    And the projection of the index onto the 17 IDs matches the golden byte for byte
    And none of the 17 IDs is under "DH-S-16", "DH-S-17" or "DH-S-18"
