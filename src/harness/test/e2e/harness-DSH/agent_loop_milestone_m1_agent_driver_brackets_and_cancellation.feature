@story-harness:DSH @phase-agent-loop-milestone-m1 @stage-agent-driver-brackets-and-cancellation @setup-compose
Feature: Agent driver brackets and cancellation
  The agent driver brackets every model call in a step and every step in a
  turn, answers every tool call on every exit, resumes a step cancel at the
  continue-or-stop decision (point 14), and closes a turn on its step budget.
  Every scenario drives the real driver on the scripted fake model, the memory
  store and in-process tools; nothing is skipped.

  Scenario: No tool call left unanswered on any exit
    Given random scripts of model replies with tool calls and a random exit injected from "cancel, budget, provider-error, lease-loss"
    When 500 property cases run
    Then every case's history fold has a result for every tool call
    And every case's brackets balance
    And every exit kind "none, cancel-in-model, cancel-in-tool, budget, provider-error, lease-loss" was injected at least once
    And at least one lease-loss case cut the driver off and the next write-open closed its brackets

  Scenario: Cancel step resumes at the continue-or-stop decision
    Given a running turn in step 2 on the scripted fake model
    And a test hook that records each time the driver reaches point 14
    And one user message "parked" parked in the inbox
    When Cancel is called with cause "user", scope "step" and inbox "keep"
    Then step 2's attempt settles "cancelled"
    And step/end for step 2 records cause "user" from the closed cause set "user, parent, extension, disposed, budget"
    And no turn/end is written by the cancel
    And the point-14 hook fired once for step 2 of the same turn
    And the driver opened step 3 of the same turn with the parked message
    When a second run with an empty inbox and a scripted final answer in step 2 is cancelled with scope "step" at point 14
    Then the second run's point-14 hook fired for step 2
    And the second run's turn ended "completed" without a step 3

  Scenario: Turn budget closes the turn
    Given a step budget of 3
    And a model that always calls a tool
    When the turn runs
    Then exactly 3 steps run
    And turn/end records cause "budget" with limit "steps" and value 3
    And the turn's brackets balance and every tool call has a result
