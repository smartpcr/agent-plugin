@story-harness:DSH @phase-boot-launch-egress-secrets-and-test-kit @stage-harness-test-kit-and-circle-closure @setup-inline
Feature: Harness test kit and circle closure
  The test kit composes real harness components against scripted boundaries: a
  virtual clock, a fake model behind a fake transport, private temp homes and
  re-executed child processes, every one released by test cleanup. The DH-S-6
  scenario files are authored, indexed and kept out of the default gate run.

  Scenario: Virtual clock drives deadlines without sleeping
    Given a test-kit tree with a component waiting 30 s on the virtual clock
    When the clock is advanced 1 ns short of 30 s
    Then the wait has not completed
    When the clock is advanced the last 1 ns
    Then the wait completes at the 30 s deadline
    And the advance took under 100 ms of wall time

  Scenario: Fake model scripts a turn
    Given a session with an open step on route "r/a"
    And the fake model scripted with one text reply "scripted hello"
    When the runtime makes one attempt through the fake transport
    Then one "assistant/message" is settled with the scripted text
    And the fake transport saw one streaming request for route "r/a"

  Scenario: Temp home torn down by cleanup
    Given a test that creates a test-kit temp home and a child process
    And the home passes the boot permission check while the test runs
    And the child process is running with its working directory in the home
    When the test ends
    Then the test passed
    And the home directory no longer exists
    And no child process remains

  Scenario: DH-S-6 scenarios authored and indexed
    Given the four new scenario files and "test/harness/acceptance-index.yaml"
    When the gate's index check runs
    And "go -C src/harness vet -tags scenario ./..." runs
    Then "DH-E-EXEC-1" and "DH-E-HOST-4" sit under "DH-S-18"
    And "DH-E-BOOT-1" sits under "DH-S-22"
    And "DH-E-UTIL-1" sits under "DH-S-39"
    And each of the four IDs has exactly one scenario file
    And the gate's index check passes
    And vet exits 0
    And the default gate run executes none of them
