@story-harness:DSH @phase-boot-launch-egress-secrets-and-test-kit @stage-preview-validated-reload-and-settings-edits @setup-inline
Feature: Preview, validated reload and settings edits
  Layers can be previewed without starting anything, a bad layer edit is
  rejected whole at reload with a positioned diagnostic, and two settings
  clients editing from the same read never lose a write.

  Scenario: Preview without start (TC-06a)
    Given a bundle layer and a user layer held in memory
    When preview runs over the layers
    Then the preview reports no issues
    And the preview lists entries "probe" and "quiet" in order
    And entry "probe" is owned by layer "bundle:core" with composed config {"level":"debug","port":1}
    And field "config.level" of entry "probe" is owned by layer "user" at "user.yml:4:"
    And field "config.port" of entry "probe" is owned by layer "bundle:core"
    And no component's construct function was called

  Scenario: Bad layer edit leaves running tree unchanged (TC-06b)
    Given a temp home whose profile layer defines entries "probe" and "other"
    And a running tree booted from the home's layers
    When the user layer is rewritten with an invalid level and reload fires
    Then reload is rejected with reason "config/rejected"
    And the diagnostic names the user layer at line 6 and path "probe.config.level"
    And every component's effective config is unchanged
    And no component was constructed again and the revision is unchanged
    When the user layer is fixed and reload fires
    Then reload succeeds and entry "other" has effective port 2

  Scenario: Concurrent settings edits do not lose a write (TC-06b)
    Given a temp home whose profile layer defines entries "probe" and "other"
    And two settings clients on the home
    When both clients edit different keys from the same read at once
    Then one edit is applied and the other is refused as stale
    When the stale client reads again and retries its edit
    Then the retry is applied
    And the profile layer on disk holds level "debug" for "probe" and port 9 for "other"
    And the profile layer on disk keeps its comment
