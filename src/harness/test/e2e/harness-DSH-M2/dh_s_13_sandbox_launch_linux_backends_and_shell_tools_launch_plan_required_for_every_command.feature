@story-harness:DSH-M2 @phase-dh-s-13-sandbox-launch-linux-backends-and-shell-tools @stage-launch-plan-required-for-every-command @setup-inline
Feature: Launch plan required for every command (DH-S-13, Stage 3.2)
  The one process starter refuses a Spec without a sandbox.Launch before
  anything starts, executes a staged plan itself (Wrap, start, Confirm,
  Release), and the signed-in client routes start under the explicit
  unconfined helper plan ForHelper issues (TC-04a, TC-04b, DH-D-EXEC-4).

  Scenario: Spec without a plan refused
    Given a subprocess Spec whose argv is the recording fake spawner and which has no Launch
    When Start is called
    Then it returns an herr error with reason "subprocess/no-launch"
    And the fake spawner records zero starts
    And the same Spec with a launch plan records exactly one start

  Scenario: Rejected confirm leaves no command
    Given a real re-exec launch stage whose command writes a marker and a Confirm hook that rejects with "sandbox/enforcement-insufficient"
    When Start runs the staged plan
    Then the marker is absent
    And the outcome is an herr error with reason "sandbox/enforcement-insufficient" and no handle
    And the Confirm hook saw the stage's ready for backend "none"
    And Release ran once

  Scenario: Client routes still start
    Given the existing claudecode fake-CLI tests
    When they run after the change
    Then they pass
    And each recorded spawn carries a helper plan
