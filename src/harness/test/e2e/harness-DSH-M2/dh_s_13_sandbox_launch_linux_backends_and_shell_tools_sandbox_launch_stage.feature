@story-harness:DSH-M2 @phase-dh-s-13-sandbox-launch-linux-backends-and-shell-tools @stage-sandbox-launch-stage @setup-inline
Feature: Sandbox launch stage (DH-S-13, Stage 3.1)
  The hidden `dsh sandbox-launch` stage sends ready over its inherited pair and
  becomes the command only after the host's go (TC-04a, TC-04b, INV-14).

  Scenario: No command before go
    Given the test binary re-executed as "sandbox-launch" with the "none" backend and a marker-writing helper as the command
    When the host reads ready and then closes the channel without go
    Then the marker file never exists
    And the stage exits non-zero

  Scenario: Unconfined plan reaches go
    Given a "danger-full-access" plan with backend "none"
    When the host answers go
    Then the marker exists
    And the reported confinement is "Unconfined" with canary "not-tried"

  Scenario: Ready message decoding
    Given byte strings for valid and truncated ready frames
    When the decoder parses them
    Then valid frames yield the backend and effects
    And truncated frames return "sandbox/launch-failed"
