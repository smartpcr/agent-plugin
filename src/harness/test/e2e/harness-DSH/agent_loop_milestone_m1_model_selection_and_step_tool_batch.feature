@story-harness:DSH @phase-agent-loop-milestone-m1 @stage-model-selection-and-step-tool-batch @setup-compose
Feature: Model selection and step tool batch (Stage 11.1)
  Each step captures the model it runs with and logs that capture as the
  model/selection event right after its step/start; the step's settlement is
  requested on the captured route (DH-D-AGENT-7, DH-ADR-AGENT-10). A step's
  tool calls run as one bounded batch: calls whose resource keys conflict never
  overlap, at most maxParallel bodies run at once, and results commit in the
  model's order whatever order the bodies finish in (DH-D-AGENT-10).

  @proof-in-process @TC-11a @DH-T-AGENT-23
  Scenario: Model selection logged per step
    Given an agent on the memory store whose default model is route "r/a"
    And routes "r/a" and "r/b" are served by the scripted fake model
    When step 1 runs
    And the agent's default model changes to route "r/b"
    And step 2 runs
    Then each step/start is followed by a model/selection naming the model the step used
    And each step's settlement was requested on the route its model/selection names
    And the scripted fake model was called on routes "r/a,r/a"
    And step 1 was captured on route "r/a" from source "default"
    And step 2 was captured on route "r/a" from source "header"

  @proof-in-process @TC-11a @DH-T-AGENT-23
  Scenario: A select moves the next step's model and its settlement
    Given an agent on the memory store whose default model is route "r/a"
    And routes "r/a" and "r/b" are served by the scripted fake model
    When step 1 runs
    And the agent selects route "r/b"
    And step 2 runs
    Then each step/start is followed by a model/selection naming the model the step used
    And each step's settlement was requested on the route its model/selection names
    And the scripted fake model was called on routes "r/a,r/b"
    And step 2 was captured on route "r/b" from source "select"

  @proof-in-process @TC-11b @DH-T-AGENT-26
  Scenario: Conflicting calls serialize, results keep model order
    Given the SQLite store in a temp home
    And a guarded pipeline with maxParallel 3
    And tool "touch" that writes the resource named by its "key" argument
    And the step's tool set is resolved for the agent
    When the model calls "touch" three times with keys "a,b,a" and call 2 finishes first
    Then the bodies finished in order "2,1,3"
    And calls 1 and 3 never overlapped
    And calls 1 and 2 overlapped
    And no more than 3 bodies ran at once
    And the batch returned results in order "c1,c2,c3"
    And a second connection to the database reads tool/call rows in order "c1,c2,c3" and tool/result rows in order "c1,c2,c3"
