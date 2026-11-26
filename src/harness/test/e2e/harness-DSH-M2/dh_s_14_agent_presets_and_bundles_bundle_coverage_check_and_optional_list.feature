@story-harness:DSH-M2 @phase-dh-s-14-agent-presets-and-bundles @stage-bundle-coverage-check-and-optional-list @setup-inline
Feature: Bundle coverage check and optional list
  Every catalogue component is mounted by a shipped layer or listed, with an
  owner and a reason, in bundles/optional.yml -- never both, and the list names
  only catalogue components (DH-ADR-BUNDLE-10). No default profile, composed
  from its shipped bundles with no route configured, sends anything (NG-7).

  # TC-08a [proof: in-process; deps: none]
  Scenario: Unbundled component fails coverage
    Given the committed optional list "bundles/optional.yml"
    And the shipped build catalogue passes the coverage check
    And an in-memory catalogue of the shipped build plus component "boot.x" in no profile
    When the coverage check runs
    Then it fails with reason "profile/uncovered"
    And the failure names "boot.x" as mounted by no shipped layer and not on the optional list
    And the failure names no other unmounted component
    When "boot.x" is added to the optional list with owner "A21" and reason "planted by the e2e"
    And the coverage check runs
    Then it passes

  # TC-08a [proof: in-process; deps: none]
  Scenario Outline: Defaults send nothing
    Given the default profile "<profile>" composed from its shipped bundles with no route configured
    When the egress set is computed
    Then the egress set is empty
    And configuring a "model-request" route to "https://api.example.test" on it puts exactly that kind in the egress set

    Examples:
      | profile     |
      | admin       |
      | core        |
      | web         |
      | headless    |
      | sdk         |
      | sdk-minimal |
      | acp         |
