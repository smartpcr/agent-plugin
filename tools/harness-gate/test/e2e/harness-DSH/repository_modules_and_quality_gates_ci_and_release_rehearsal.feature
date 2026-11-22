@story-harness:DSH @phase-repository-modules-and-quality-gates @stage-ci-and-release-rehearsal @setup-inline
Feature: CI and release rehearsal (Stage 1.4)
  The keyless harness job gates every commit on hosted Linux and Windows, as
  regression signals (platforms-windows-wsl: the evidence of record is the local
  Windows and WSL lanes, and macOS is out of M1 scope); the release rehearsal
  builds six pure-Go binaries and publishes nothing; the scenario workflow is
  guarded by a preflight that decides from its environment alone and reads no
  model key (models-signed-in-claude-copilot); authored scenarios are indexed,
  compile, and never run in the default gate.

  Scenario: Preflight passes with no model key
    Given an in-memory acceptance index with "DH-E-X-1" under "DH-S-18" authored at "DH-S-1" and its scenario test
    And an injected environment map with no model variables and DSH_REQUIRE_SCENARIOS ""
    When the preflight decision function runs
    Then the preflight passes with an empty plan
    And the preflight subcommand on the repository with no model variables and DSH_REQUIRE_SCENARIOS "" exits 0 and plans 0 scenarios

  Scenario: Preflight fails on an unknown required scenario
    Given an in-memory acceptance index with "DH-E-X-1" under "DH-S-18" authored at "DH-S-1" and its scenario test
    And an injected environment map with no model variables and DSH_REQUIRE_SCENARIOS "DH-E-FAKE-9"
    When the preflight decision function runs
    Then it returns a failure naming "DH-E-FAKE-9"
    And no scenario is started
    And the preflight subcommand on the repository with no model variables and DSH_REQUIRE_SCENARIOS "DH-E-FAKE-9" exits 1 naming "DH-E-FAKE-9" and writes no scenario plan
    When DSH_REQUIRE_SCENARIOS is empty and the preflight decision function runs
    Then the preflight passes with an empty plan

  Scenario: Rehearsal produces six pure-Go binaries and publishes nothing
    Given GOWORK=off, a parent environment with CGO_ENABLED=1 and GOFLAGS=-race, and no network
    When harness-gate rehearse --out a temporary directory runs on the repository
    Then the rehearsal exits 0 and reports "nothing published"
    And the temporary directory holds exactly the six binaries for linux, darwin, windows on amd64 and arm64
    And each binary's embedded build info records CGO_ENABLED=0, -trimpath and its own GOOS and GOARCH
    And the printed SHA-256 of each binary matches its content
    And the repository's tags are unchanged

  Scenario: Gate passes on both hosted CI operating systems
    Given the story branch at the stage-closing commit
    And the harness job of ci.yml runs the gate on ubuntu-latest and windows-latest with CGO_ENABLED=1
    And a C compiler is available for -race on this host's lane
    When the harness job's gate runs on this host's lane
    Then this lane's gate output ends with every gate passed
    And the DH-S-1 evidence record names the run id and a passing result for each of ubuntu-latest and windows-latest

  Scenario: Authored scenarios are indexed and compile but do not run
    Given "src/harness/internal/testkit/scenarios/toolchain_rehearsal_scenario_test.go" and "test/harness/acceptance-index.yaml" in the checkout
    When the gate's scenario-index check runs on the repository
    And "go -C src/harness vet -tags scenario ./..." runs
    Then the scenario-index check passes
    And every *_scenario_test.go ID appears in exactly one index entry
    And "DH-E-TOOLCHAIN-2" is indexed under "DH-S-18"
    And every index ID authored at or before the built-through stage has a scenario file
    And the vet exits 0
    And the default gate go test run lists zero scenario tests
    And a "-tags scenario" listing does list "TestToolchainRehearsal"
