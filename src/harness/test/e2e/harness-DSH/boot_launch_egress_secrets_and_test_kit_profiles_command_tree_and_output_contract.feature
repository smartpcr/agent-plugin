@story-harness:DSH @phase-boot-launch-egress-secrets-and-test-kit @stage-profiles-command-tree-and-output-contract @setup-inline
Feature: Profiles, command tree and output contract
  A launch composes its configuration from a layer stack where the higher layer
  wins and the ownership index names the winner; the one command tree maps usage
  errors to exit status 2 and generates the CLI reference committed as a golden.

  # TC-06a [proof: in-process; deps: none]
  Scenario: Layer stack precedence
    Given an in-memory bundle layer setting model "a"
    And a profile layer setting model "b"
    And an empty user layer
    When the stack composes
    Then the composed model is "b"
    And the ownership index names the profile layer as owner of the model
    And the ownership index names the bundle layer as owner of the entry

  # TC-06a [proof: in-process; deps: none]
  Scenario: Usage error exits 2
    Given the command tree
    When "dsh --no-such-flag" is dispatched in process with captured streams
    Then the mapped exit code is 2
    And stderr holds one usage line naming "--no-such-flag" plus the help hint
    And stdout is empty

  # [proof: golden; deps: committed golden internal/cli/testdata/reference.golden]
  Scenario: Generated CLI reference is current
    Given the command tree
    When the reference is generated
    Then it equals the committed golden "internal/cli/testdata/reference.golden"
    And "dsh catalog commands" prints the same reference with exit code 0
