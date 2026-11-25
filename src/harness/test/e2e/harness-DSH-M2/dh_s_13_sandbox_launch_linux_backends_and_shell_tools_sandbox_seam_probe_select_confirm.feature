@story-harness:DSH-M2 @phase-dh-s-13-sandbox-launch-linux-backends-and-shell-tools @stage-sandbox-seam-probe-select-confirm @setup-inline
Feature: Sandbox seam -- probe, select, confirm
  The local sandbox seam probes its backend chain at start-up, selects the first
  backend that passed, prepares the plan for a policy and, on the launch stage's
  ready, makes the four-step host decision before the command may run. Each start
  below runs through the one starter: a re-executed launch stage speaks the real
  ready/go channel, and the command it becomes writes a marker, so the marker
  exists exactly when the decision accepted the start.

  Scenario Outline: Four-step host decision (TC-04a) -- <row>
    Given a local sandbox service whose fake "bwrap" backend passed its start-up probe governing every effect
    And a "<policy>" policy
    And a launch stage that reports backend "<backend>", governed "<governed>" and canary "<canary>"
    When Confirm evaluates the stage's ready
    Then the host decision is "<decision>" with "<result>"
    And exactly one launch stage was started
    And the chain was re-probed <reprobes> times

    Examples:
      | row                           | policy                          | backend | governed                                            | canary    | decision | result                           | reprobes |
      | confined, canary written      | workspace-write                 | bwrap   | fs-write,network,harness-home,delegated-execution | written   | reject   | sandbox/launch-failed            | 1        |
      | confined, canary not tried    | workspace-write                 | bwrap   | fs-write,network,harness-home,delegated-execution | not-tried | reject   | sandbox/launch-failed            | 1        |
      | unconfined, canary not tried  | danger-full-access              | none    |                                                     | not-tried | accept   | unconfined                       | 0        |
      | unconfined under require full | danger-full-access require full | none    |                                                     | not-tried | reject   | sandbox/enforcement-insufficient | 0        |

  Scenario: Weaker evidence wins (TC-04a)
    Given a local sandbox service whose fake "bwrap" backend passed its start-up probe governing every effect
    And the start-up probe reports enforcement "full" for a "workspace-write" policy that denies the network
    And a "workspace-write" policy
    And a launch stage that reports backend "bwrap", governed "fs-write,harness-home,delegated-execution" and canary "refused"
    When Confirm evaluates the stage's ready
    Then the host decision is "accept" with "partial"
    And the confinement is missing exactly "network"
    And exactly one launch stage was started
    And the chain was re-probed 0 times

  Scenario: Windows refuses confined start (TC-06b row on Windows)
    Given the Windows lane's local sandbox service
    And a "workspace-write" policy
    When Prepare runs for the marker command
    Then Prepare returns "sandbox/unavailable" and no plan
    And starting the command without a plan is refused and starts no process
