@story-harness:DSH @phase-repository-modules-and-quality-gates @stage-gate-runner-core @setup-inline
Feature: Gate Runner Core
  harness-gate runs one fixed, ordered list of gates, stops at the first failure,
  names it and exits non-zero (QG-1). Coverage is merged across processes and held
  to an 85% per-package floor (QG-3); the project's own analyzers forbid sleeping
  in tests (QG-2, QG-4).

  @TC-01a @proof-in-process
  Scenario: First failing gate is named
    Given an in-memory gate list "one,two,three,four,five" whose gate "three" returns an error
    When the runner executes
    Then gates "one,two,three" ran
    And gates "four,five" never ran
    And stdout names gate "three" as the failure
    And the exit code is non-zero

  @TC-01a @proof-in-process
  Scenario: Coverage floor arithmetic over merged profiles
    Given an in-memory parent coverage profile for package "example.com/m/p" covering 35 of 50 statements
    And an in-memory re-executed child profile for the same package covering 9 further statements
    When the floor check runs over both profiles
    Then package "example.com/m/p" is at 88% and passes the floor
    When the floor check runs over the parent profile only
    Then the floor check fails naming "example.com/m/p: 70% < 85%"

  @TC-01a @proof-in-process
  Scenario: Analyzer flags sleep in a test file
    Given source "func TestX(t *testing.T){ time.Sleep(1) }" parsed with go/parser.ParseFile from a string as "x_test.go"
    When the nosleep predicate runs
    Then exactly one diagnostic is returned at the call position

  @TC-01b @proof-service-go-toolchain
  Scenario: Seeded data race fails the race gate
    Given the seeded module "race-seeded" copied into a temporary directory
    When harness-gate runs against it
    Then the run stops at gate "test" and names it
    And the output mentions "data race detected"
    And the exit code is non-zero

  @TC-01b @proof-service-go-toolchain
  Scenario Outline: Seeded unformatted file and low-coverage package
    Given the seeded module "<module>" copied into a temporary directory
    When harness-gate runs against it
    Then the run stops at gate "<gate>" and names it
    And the output mentions "<detail>"
    And the exit code is non-zero

    Examples:
      | module           | gate     | detail                         |
      | unformatted-file | gofmt    | add.go                         |
      | coverage-40pct   | coverage | example.com/cov40/p: 40% < 85% |

  @TC-01b @proof-service-go-toolchain
  Scenario: Own analyzers against fixture packages
    Given the committed analyzer fixtures under "analyzers/testdata"
    When analysistest.Run loads them for every own analyzer
    Then every expected diagnostic is reported and none extra
    And at least one diagnostic was reported by each of "nosleep,onespawn,cleanup"
