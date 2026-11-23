@story-harness:DSH @phase-tool-pipeline-approvals-and-human-commands @stage-approvals-and-guarded-pipeline @setup-compose
Feature: Approvals and guarded pipeline
  Every tool call runs the guarded pipeline in its fixed order -- resolve,
  validate, policy, guards, approval, flush, body, shape -- and a call stopped at
  one stage runs none of the later ones except shape, which appends its one
  tool/result. Denial is enforced in the executor: nobody to ask means no.

  # DH-T-TOOLS-3, TC-10a [proof: in-process]
  Scenario: Schema-breaking arguments never reach the body
    Given a booted guarded pipeline with no approval service mounted
    And tool "add(a:int,b:int)" is registered with a body counter
    When the model calls "add" with arguments '{"a":"x"}'
    Then the body counter is 0
    And the call ends "tools/invalid-arguments" at the "validate" stage
    And the session log after the step holds "tool/call,tool/result"
    And the tool/result returned to the model is an error starting with "tools/invalid-arguments: "

  # DH-S-10 exit evidence, SP-7 [proof: in-process]
  Scenario: Nobody to ask means no, through the executor
    Given a booted guarded pipeline with no approval service mounted
    And tool "add(a:int,b:int)" is registered with a body counter
    And a policy that asks for approval of every call
    And the step's assembled prompt lists tool "add"
    When the model calls "add" with arguments '{"a":1,"b":2}'
    Then the approval outcome is "unavailable"
    And the call ends "approval/unavailable" at the "approval" stage
    And the session log after the step holds "tool/call,approval/asked,approval/decided,tool/result"
    And the logged approval/decided has outcome "unavailable" attributed to a principal
    And the body counter is 0

  # DH-T-TOOLS-4 [proof: in-process]
  Scenario: Pipeline stage order fixed
    Given a booted guarded pipeline with recording stubs at every stage and an answerer that approves
    And tool "add(a:int,b:int)" is registered with a body counter
    When the model calls "add" with arguments '{"a":1,"b":2}'
    Then the recorded order is "resolve,validate,policy,guards,approval,flush,body,shape"
    And the call succeeds with text "3" and approval outcome "allowed-once"
    And the session log after the step holds "tool/call,approval/asked,approval/decided,tool/result"

  # DH-T-TOOLS-3 [proof: in-process]
  Scenario: Unknown or unadmitted tool refused before execution
    Given a booted guarded pipeline with recording stubs at every stage and an answerer that approves
    And tools "echo" and "grep" are registered
    And the step's fixed ToolSet admits only "echo"
    When the model calls "rm" with arguments '{"path":"/"}' and then "grep" with arguments '{"text":"x"}'
    Then each call ends "tools/not-started" at the "resolve" stage
    And the refusal of "rm" says "not a registered tool"
    And the refusal of "grep" says "not a tool of this step"
    And the validate, policy, guards, approval, flush, body and shape stubs record zero invocations
    And the session log after the step holds "tool/result,tool/result"

  # TC-10b [proof: service:sqlite]
  Scenario: Flush before body observed in the database
    Given the SQLite store in a temp home
    And a booted guarded pipeline with recording stubs at every stage and an answerer that approves
    And tool "add(a:int,b:int)" whose body reads the database from a second connection
    When the model calls "add" with arguments '{"a":1,"b":2}'
    Then the call succeeds with text "3" and approval outcome "allowed-once"
    And the second connection saw "tool/call,approval/asked,approval/decided" before the body's first instruction
    And the second connection saw no "tool/result" before the body's first instruction
    And the recorded order is "resolve,validate,policy,guards,approval,flush,body,shape"
