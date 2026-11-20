@story-harness:DSH @phase-boot-launch-egress-secrets-and-test-kit @stage-launch-facts-home-layout-and-secrets @setup-inline
Feature: Launch facts, home layout and secrets
  Boot fixes the launch facts before the component tree exists, lays out a home
  private to its owner, and resolves secrets per use without ever printing them.

  # TC-06b [proof: service:filesystem; deps: filesystem via t.TempDir()]
  Scenario: Home created private
    Given an empty temporary directory as the home
    When the layout check runs
    Then every directory of the layout exists private to its owner
    When the test loosens "spill/" for this operating system
    And the layout check runs again
    Then the check fails as not private naming "spill/"

  # TC-06a [proof: in-process; deps: none]
  Scenario: Secret resolved per use and redacted
    Given a fake secret source returning "sk-test-123"
    And configuration naming the reference "secret://deepseek/api-key"
    When the reference is resolved for a use that fails echoing the value
    And the error and log lines of the failed use are captured
    Then the value was used exactly once
    And the source was read exactly once
    And "sk-test-123" appears in no captured string
    And the captured error names the reference "secret://deepseek/api-key"
    And the captured logs show the redaction marker

  # TC-06a [proof: in-process; deps: none]
  Scenario: Launch facts feed the expression scope
    Given in-memory launch facts with cwd "/w" and OS "linux"
    When "${{ launch.os }}" is evaluated
    Then it yields "linux"
    And "launch.cwd" yields "/w"
