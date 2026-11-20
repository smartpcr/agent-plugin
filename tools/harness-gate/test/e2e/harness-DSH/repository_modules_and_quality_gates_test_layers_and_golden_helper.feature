@story-harness:DSH @phase-repository-modules-and-quality-gates @stage-test-layers-and-golden-helper @setup-inline
Feature: Test layers and golden helper
  The project's one golden helper (src/harness/internal/testkit/golden) rewrites a
  golden only under the single -update switch; the fuzzcheck analyzer requires a
  fuzz target in every package marked //dsh:untrusted-parser; and the keyless test
  gate of tools/harness-gate never runs scenario-tagged tests (DH-T-TOOLCHAIN-4,
  QG-6, DH-ADR-TOOLCHAIN-4).

  # proof: service:filesystem; deps: filesystem via a temporary directory
  Scenario: Golden helper detects drift and updates only on the switch
    Given a golden file "drift" in a temporary directory with content "a"
    When golden.Assert is called for "drift" with "b" without -update
    Then the golden test fails with a diff from "a" to "b"
    And the golden file "drift" still holds "a"
    When golden.Assert is called for "drift" with "b" with -update
    Then the golden test passes
    And the golden file "drift" holds "b"

  # proof: in-process; deps: none
  Scenario: Missing fuzz target is reported
    Given in-memory source of a package marked //dsh:untrusted-parser with no Fuzz function
    When the fuzzcheck predicate runs on the parsed AST
    Then it reports exactly one diagnostic naming the missing fuzz target
    When "func FuzzDecode(f *testing.F)" with compiled-in seeds is added in seeds_test.go
    And the fuzzcheck predicate runs on the parsed AST
    Then it reports no diagnostic

  # proof: service:go-toolchain; deps: go-toolchain; filesystem via a temporary directory
  Scenario: Scenario-tagged tests are excluded from the default gate
    Given a scratch module containing "x_scenario_test.go" with "//go:build scenario" that always fails
    When the harness-gate test gate runs on the scratch module
    Then the test gate passes
    And the test gate output does not mention "TestScenarioAlwaysFails"
    When the harness-gate test gate runs on the scratch module with GOFLAGS "-tags=scenario"
    Then the test gate passes
    When go test runs on the scratch module with "-tags scenario"
    Then go test fails with "TestScenarioAlwaysFails"
