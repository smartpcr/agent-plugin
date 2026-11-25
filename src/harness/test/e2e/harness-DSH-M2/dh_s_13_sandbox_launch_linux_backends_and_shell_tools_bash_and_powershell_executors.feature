@story-harness:DSH-M2 @phase-dh-s-13-sandbox-launch-linux-backends-and-shell-tools @stage-bash-and-powershell-executors @setup-inline
Feature: Bash and PowerShell executors (DH-S-13, TC-06b)
  The one-shot shell executors run a command through the sandbox seam and the
  one starter, report the exit code, the bounded stdout tail and the confinement
  the launch plan was confirmed under, and build the child environment so that
  no name the secret store holds ever reaches the child.

  Scenario: bash executor on Linux
    Given the Linux lane with bash found by preflight
    And a danger-full-access test layer
    When Start runs "echo hi; exit 3" on the bash executor
    Then the outcome kind is "exited"
    And the outcome has exit code 3
    And the stdout tail is "hi"
    And the confinement is "Unconfined"

  Scenario: pwsh executor on Windows
    Given the Windows lane with pwsh found by preflight
    And a danger-full-access test layer
    When Start runs "Write-Output hi; exit 3" on the pwsh executor
    Then the outcome kind is "exited"
    And the outcome has exit code 3
    And the stdout tail is "hi"
    And the confinement is "Unconfined"

  Scenario: Secrets never reach the child
    Given a secret named "DSH_TEST_TOKEN" stored in human/secret
    And "DSH_TEST_TOKEN" is present in the parent environment
    And "BPE_PLAIN_MARKER" is present in the parent environment
    When the env builder runs with the call entry "DSH_TEST_TOKEN"
    Then the child environment lacks "DSH_TEST_TOKEN"
    And no secret value reaches the child environment
    And the child environment keeps "BPE_PLAIN_MARKER"
