@story-harness:DSH @phase-loader-layers-and-write-back @stage-layer-composition-and-write-back @setup-inline
Feature: Layer composition and write-back
  Layers compose lowest first into one effective tree with an ownership index naming
  the layer that supplied each value. A runtime edit is written back to the single
  writable layer file only, keeping its comments, and a write guard refuses to
  overwrite a file another writer changed since the layer set read it.

  # DH-T-LOADER-7 (TC-03a)
  Scenario: Ownership index names the supplying layer
    Given an in-memory layer "base" inserting entry "svc" with config {"a":1,"b":1}
    And an in-memory layer "user" merging {"b":2} into entry "svc"
    When the layers are composed
    Then there are no composition issues
    And the effective config of entry "svc" is {"a":1,"b":2}
    And the index reports field "config.a" of "svc" from layer "base"
    And the index reports field "config.b" of "svc" from layer "user"

  # DH-T-LOADER-8 (TC-03b)
  Scenario: Write-back touches one layer file only
    Given base and user layer files in a temporary directory with "user" writable
    And the running config of entry "svc" is {"a":1,"b":2}
    When "b" of entry "svc" is set to 3 and the layer set writes back
    Then the write-back succeeds
    And the "user" layer file equals the committed golden "writeback_user"
    And the "user" layer file still holds its comments
    And the "base" layer file is byte-identical to before

  # DH-T-LOADER-15 (TC-03b)
  Scenario: Write guard refuses a stale write
    Given base and user layer files in a temporary directory with "user" writable
    And another writer modifies the "user" layer file on disk after the layer set read it
    When "b" of entry "svc" is set to 3 and the layer set writes back
    Then the write fails with a stale-source error for layer "user"
    And the "user" layer file holds the other writer's content
    And the "base" layer file is byte-identical to before
