@story-harness:DSH @phase-loader-layers-and-write-back @stage-nested-loaders-and-entry-restart @setup-inline
Feature: Nested loaders and entry restart
  A component may run a nested loader whose entries live and die with it; one entry
  can be restarted without touching its siblings, with one settled-reload notice per
  edit; and every mutation through the API is recorded in the lifecycle log in order.

  Scenario: Nested loader unloads with its owner
    Given component "C" running a nested loader with entries "a" and "b"
    And the nested entries "a" and "b" are active
    When "C" is unloaded
    Then the disposers of nested entries "a" and "b" have run before C's Wait returns
    And the nested entries were disposed before C's own effects
    And the nested loader refuses further applies

  Scenario: Restart one entry, siblings untouched
    Given three loaded entries "one", "two" and "three"
    When the config of entry "two" changes and it is restarted
    Then entry "one" has been constructed 1 time
    And entry "two" has been constructed 2 times
    And entry "three" has been constructed 1 time
    And exactly 1 settled-reload notice is emitted
    And that notice lists "two" as updated

  Scenario: Explicit restart keeps siblings and persists nothing
    Given three loaded entries "one", "two" and "three"
    When entry "two" is restarted through the loader
    Then entry "one" has been constructed 1 time
    And entry "two" has been constructed 2 times
    And entry "three" has been constructed 1 time
    And exactly 1 settled-reload notice is emitted
    And that notice lists "two" as restarted
    And no change to persist was reported
    And the lifecycle log for "two" is "add,restart"

  Scenario: Lifecycle log records mutations in order
    Given a loader with an empty group "g"
    When entry "x" is added under "g" through the mutation API
    And entry "g:x" is set to text "2" through the mutation API
    And entry "g:x" is removed through the mutation API
    Then the lifecycle log for "g:x" is "add,update,remove"
    And lifecycle observers saw "add g:x,update g:x,remove g:x"
    And the lifecycle sequence numbers are strictly increasing
    And the last lifecycle event carries the removed entry
