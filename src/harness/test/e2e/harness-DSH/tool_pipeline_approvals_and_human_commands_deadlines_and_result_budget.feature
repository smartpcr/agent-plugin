@story-harness:DSH @phase-tool-pipeline-approvals-and-human-commands @stage-deadlines-and-result-budget @setup-compose
Feature: Deadlines and result budget
  A tool body runs under its budget: when the budget ends its context is
  cancelled, and a body that does not return within grace is detached so it
  never holds the turn. No result larger than maxResultBytes reaches the model:
  the excess is spilled whole through the spill store and the model gets a
  bounded preview with a reference to the saved file.

  # DH-T-TOOLS-8, DH-T-TOOLS-9, TC-10a [proof: in-process; deps: none -- virtual clock]
  Scenario: Deadline does not hold the turn
    Given a guarded pipeline on the virtual clock with a 2 s grace over the memory store
    And tool "stuck" whose body ignores cancellation with a 10 s deadline
    And tool "add(a:int,b:int)" is registered
    When the model calls "stuck" and then "add" with arguments '{"a":1,"b":2}'
    And the body of "stuck" has started and the virtual clock advances 12 s
    Then the call of "stuck" is answered timed out after "10s" with cause "deadline"
    And the step continues: the call of "add" succeeds with text "3"
    And the session log after the step holds "tool/call,tool/result,tool/call,tool/result"
    And the body of "stuck" is listed as detached for the step's agent
    And a further call of "stuck" by the same agent ends "tools/busy" at the "body" stage
    When the detached body of "stuck" finally returns
    Then the session log records a tool/late-return for "stuck" and its late value never reaches a tool/result

  # DH-T-STORAGE-10, DH-T-STORAGE-12, TC-10b [proof: service:sqlite; deps: sqlite embedded; filesystem via t.TempDir() home]
  Scenario: Oversized result spilled before reaching the model
    Given the SQLite store and the spill store in a temp home
    And a guarded pipeline with maxResultBytes of 64 KB and the spill policy mounted
    And tool "big" returning 2 MB of text
    When the model calls "big" with arguments '{}'
    Then the result returned to the model is within 64 KB with a spill reference of 2 MB
    And the preview keeps the head and the tail of the full result and names the spill file
    And a second connection to the database sees the logged tool/result within 64 KB with the same spill reference
    And the spill file in the temp home holds all 2 MB of the result
