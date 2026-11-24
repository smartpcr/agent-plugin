@story-harness:DSH-M2 @phase-dh-s-14-agent-presets-and-bundles @stage-profile-definitions-and-bundle-membership @setup-inline
Feature: Profile definitions and bundle membership
  The five profile definitions (admin, core, web, headless, sdk) stack their
  embedded bundles in order; composing each through the real codec and loader
  reproduces the committed composed.golden, and a composition lacking an entry
  of its Required list fails naming the missing component.

  # TC-08g [proof: golden; deps: committed internal/boot/profile/testdata/composed.golden]
  Scenario: Composed profiles match the golden
    Given the five profile definitions and their embedded bundles
    When the composer renders each profile
    Then the definitions are admin, core, web, headless and sdk in that order
    And the rendered output equals the committed composed.golden

  # TC-08a [proof: in-process; deps: none]
  Scenario: Required component missing fails composition
    Given the headless profile definition
    And an in-memory headless bundle holding every Required entry except the carrier
    When composition runs
    Then composition fails with reason "profile/missing-required"
    And the failure names the missing carrier component
    And the failure names no other Required entry
