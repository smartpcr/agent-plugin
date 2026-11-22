@story-harness:DSH @phase-agent-loop-milestone-m1 @stage-agent-lifecycle-and-delegation-seam @setup-compose
Feature: Agent lifecycle and delegation seam
  An agent is created in one transaction and torn down in one order (T1 to
  T8): a creation that fails part way rolls back what it reached, a child
  delegated through the seam has exactly one owner and goes with it, and a
  host shutting down mid-step leaves a closed, durable log. Every scenario
  drives the real agent registry, the driver's factory and the scripted fake
  model; TC-11a runs on the memory store, TC-11b on the embedded SQLite store
  in a temp home (or DSH_E2E_STORE_HOME when the compose run mounts one).
  Nothing is skipped.

  @TC-11a @DH-T-AGENT-19
  Scenario: Failed create leaves nothing
    Given an agent registry seated with the driver's factory on the scripted fake model and the memory store
    And a creation setup whose registrations "one" and "two" succeed and whose third registration "three" fails
    When agent "a" is created
    Then the creation fails with "third fails"
    And agent "a" is neither addressable nor listed
    And no Created or Disposed notice was sent
    And session "a" holds no partial agent events
    And the registrations were rolled back in the order "two, one"
    And session "a"'s lease is free and the model was never called

  @TC-11a
  Scenario: Child torn down with its owner
    Given a delegation seam over the driver's factory on the scripted fake model and the memory store
    And components "C" and "D" are loaded
    And component "C" delegates to child agent "K" whose model streams "par" and then hangs
    And K's model request is in flight
    Then K's one owner is "C" and the children of "C" are "K"
    And component "D" cannot also own "K"
    When component "C" is unloaded
    Then K's driver goroutine had exited when the Wait of "C" returned
    And "K" is not addressable and has no owner
    And K's result was already settled "aborted" with output "par", cleanup "ok" and diagnostic "aborted parent"
    And K is disposed

  @TC-11b @DH-T-AGENT-21
  Scenario: Shutdown keeps the last events
    Given a host on the embedded SQLite store in a temp home
    And agent "s" is mid-step with its model streaming "par" and then hanging
    When the host shuts down
    And the SQLite store is reopened from its database file
    Then the reopen had nothing to close
    And the persisted log ends with closed brackets
    And the last turn/end records reason "aborted" with cause "disposed"
    And the last assistant settlement "par" is persisted before the last turn/end
