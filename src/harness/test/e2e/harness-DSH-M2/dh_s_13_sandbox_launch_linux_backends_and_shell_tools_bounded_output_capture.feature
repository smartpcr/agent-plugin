@story-harness:DSH-M2 @phase-dh-s-13-sandbox-launch-linux-backends-and-shell-tools @stage-bounded-output-capture @setup-inline
Feature: Bounded output capture (TC-05b)
  A collected stream keeps only a bounded tail in memory, saves the whole
  stream to a spill file, never drains on read, and Run's hard limit kills
  the whole process tree.

  Scenario: Flood is bounded and recoverable
    Given a re-exec helper that writes 64 MiB to stdout
    When it runs under the default ring size with a spill store in a temp dir
    Then memory held by the ring stays at its bound
    And the tail equals the last bytes written
    And the spill file holds the full stream

  Scenario: Hard limit ends the tree
    Given a helper that spawns a grandchild and sleeps
    When Run's hard limit of 2 seconds fires with a 60 second grace
    Then Run reports the hard limit within 12 seconds
    And both the child and grandchild pids are gone within 10 seconds

  Scenario: Two readers see the same tail
    Given a ring of 4 KiB with 10 KiB written
    When two readers read concurrently
    Then both readers observe identical bytes equal to the ring's tail
    And the ring is not drained
