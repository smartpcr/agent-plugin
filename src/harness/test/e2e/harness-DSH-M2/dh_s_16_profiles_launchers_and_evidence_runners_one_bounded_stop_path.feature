@story-harness:DSH-M2 @phase-dh-s-16-profiles-launchers-and-evidence-runners @stage-one-bounded-stop-path @setup-inline
Feature: One bounded stop path (TC-14b, DH-D-CLI-4)
  Ctrl-C, SIGTERM, SIGHUP, the end of standard input, `dsh stop`, the end of a
  stdio carrier's input and a stalled client all end in one call of
  launch.App.Stop, made by the lifecycle state machine (internal/cli/lifecycle).
  A second signal while stopping forces the stop: Stop is called again with
  "interrupted", the launch's shutdown wait ends at once, and the first stop's
  reason and status stay recorded.

  The built dsh (./cmd/dsh with the testhook tag, which adds the keyless fake
  route llm.testkit-route and the long fake tool testkit_sleep) running
  `dsh headless` gets the lane's termination signal during the tool call --
  SIGTERM on Linux, a closed console (CTRL_CLOSE_EVENT) on Windows -- and exits
  0 within ShutdownTimeout with the session log closing the turn.

  Scenario Outline: Every trigger reaches one Stop -- <trigger>
    Given the stop state machine watching a counting fake Stop with no busy work
    When the trigger "<trigger>" fires
    And a "dsh stop" request and the end of standard input follow it
    Then Stop was called exactly once with reason "<reason>" and status <status>
    And the state machine is "stopping"
    And the shutdown was not forced
    And the launch records reason "<reason>" and status <status>

    Examples:
      | trigger        | reason         | status |
      | ctrl-c         | interrupted    | 130    |
      | sigterm        | terminated     | 0      |
      | sighup         | terminated     | 0      |
      | stdin end      | input-closed   | 0      |
      | dsh stop       | requested      | 0      |
      | stdio end      | input-closed   | 0      |
      | stalled client | client-stalled | 125    |

  Scenario Outline: A second signal during the stop moves to forced -- <second>
    Given the stop state machine watching a counting fake Stop with no busy work
    When the trigger "ctrl-c" fires
    Then Stop was called exactly once with reason "interrupted" and status 130
    And the shutdown was not forced
    When the trigger "<second>" fires
    Then the state machine ends "forced"
    And the shutdown was forced
    And Stop was called twice, the first with reason "interrupted" and status 130 and the second with reason "interrupted"
    And the launch records reason "interrupted" and status 130

    Examples:
      | second  |
      | ctrl-c  |
      | sigterm |

  Scenario: Signal during a tool call
    Given the built dsh with the fake route calling testkit_sleep on a temp home
    And "dsh headless \"sleep, please\"" is running on the temp home
    When testkit_sleep is running
    And the lane's termination signal is sent to dsh headless
    Then dsh headless exits within ShutdownTimeout
    And the headless exit status is 0
    And the session log holds the tool call followed by a closing turn end
