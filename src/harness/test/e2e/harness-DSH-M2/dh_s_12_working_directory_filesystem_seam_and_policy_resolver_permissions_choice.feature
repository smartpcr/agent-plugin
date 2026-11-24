@story-harness:DSH-M2 @phase-dh-s-12-working-directory-filesystem-seam-and-policy-resolver @stage-permissions-choice @setup-inline
Feature: Permissions choice (Stage 2.6)
  A permission preset is one named pair of a sandbox mode and an approval
  policy. /permission switches both knobs through their owners, and a layer
  whose default preset does not resolve fails the configuration load (TC-02a).

  Scenario: Switch moves mode and approval together
    Given an in-memory live session pinned to preset "ask" of a table holding "ask" and "auto"
    When the owner runs "/permission auto"
    Then exactly one "sandbox/mode" and one "approval/policy" event are appended in that order
    And exactly one "permission/preset" event naming "auto" is appended
    And the sandbox-mode, approval and permissions views all reflect "auto"
    And the knobs in force and the current preset are "auto"

  Scenario: Unresolvable default refused
    Given a config layer whose permission.presets default preset is "ghost"
    When the configuration loads through boot
    Then the load fails with the configuration status and exactly one fatal message
    And the message names the preset "ghost" at "permissions.config.defaultPreset"
    And validating the same config directly is a configuration error naming "ghost"
