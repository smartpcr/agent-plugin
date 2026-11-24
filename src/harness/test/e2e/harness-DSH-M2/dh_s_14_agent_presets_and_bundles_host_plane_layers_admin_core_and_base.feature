@story-harness:DSH-M2 @phase-dh-s-14-agent-presets-and-bundles @stage-host-plane-layers-admin-core-and-base @setup-inline
Feature: Host-plane layers admin, core and base
  The host-plane bundles base and admin are stacked under a user layer written
  with the remove and merge operations; the admin composition is the offline
  tree A24's admin commands run in.

  # TC-08a [proof: in-process]
  Scenario: Layer operations over base then admin remove an entry, merge a map and record each value's source layer
    Given the embedded bundles "base, admin" stacked in that order
    And a user layer file that removes "sharing" and merges level "debug" into the "logging" config
    When the layers are stacked and previewed
    Then the stack order is "bundle:base, bundle:admin, profile, user, launcher"
    And the preview reports no issue
    And the composed tree holds exactly "logging"
    And the "logging" config is merged key-wise to destination "stderr" and level "debug"
    And the source of entry "logging" is layer "bundle:admin"
    And the source of "logging" field "config.destination" is layer "bundle:admin"
    And the source of "logging" field "config.level" is layer "user"
    And the removed entry "sharing" has no recorded source layer

  # TC-08g, INV-10 [proof: golden]
  Scenario: Admin profile composes to its golden section with nothing that acts
    Given the committed admin definition and its embedded bundles
    When the admin profile composes
    Then the composition reports no failure and no issue
    And the composed admin tree equals its section of the committed composed.golden
    And the composed admin tree is not empty
    And the composed admin tree holds no agent, model adapter, carrier or plugin component
