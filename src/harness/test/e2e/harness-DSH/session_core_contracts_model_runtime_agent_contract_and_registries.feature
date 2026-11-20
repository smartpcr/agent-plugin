@story-harness:DSH @phase-session-core-contracts @stage-model-runtime-agent-contract-and-registries @setup-inline
Feature: Model runtime, agent contract and registries
  The model runtime makes one attempt per call and hands its one record to the
  caller, who logs it; the agent driver's PreStep point decides a step and its
  observers cannot veto; folds are read at one cut; pending human interactions
  are addressable by agent. Everything runs in-process on memstore and fakes.

  # DH-T-LLM-2, TC-05
  Scenario: One attempt, one caller-logged settlement
    Given the runtime with an in-memory fake route that fails once then succeeds
    And an in-memory session store that records every append
    And a caller-side recording AttemptSink
    When the caller runs one Prepared.Stream attempt per scripted attempt and logs the record it receives
    Then every stream ends with exactly one Finish chunk
    And the runtime delivers exactly one attempt record per attempt carrying requestAsOf and requestDigest
    And the store holds exactly one settlement event per attempt
    And every append is attributed to the caller
    And the runtime itself made zero session appends
    When the caller runs one Complete call per scripted attempt and logs the record it receives
    Then the first Complete returns the route failure and the second returns the message
    And the runtime delivers exactly one attempt record per attempt carrying requestAsOf and requestDigest
    And the store holds exactly one settlement event per attempt
    And every append is attributed to the caller
    And the runtime itself made zero session appends

  # DH-T-AGENT-10
  Scenario: Rejected PreStep closes the turn blocked; observers cannot veto
    Given an agent.PreStep listener returning Reject with reason "quota"
    And a Notify observer that panics
    When the driver reaches point 4 of a step
    Then no "step/start" event is appended
    And "turn/end" is appended with outcome "blocked" carrying the reason "quota"
    And the observer panic is recorded without changing that outcome
    When the PreStep listener itself panics and the driver reaches point 4 of the next turn
    Then "turn/end" is appended with outcome "error" carrying "extension/failed"

  # DH-D-VIEW-1, TC-05
  Scenario: Folds read at one cut
    Given two registered folds
    And a concurrent appender
    When a reader requests both folds
    Then both results are computed at the same sequence number

  # DH-D-HUMAN-1
  Scenario: Pending interaction is addressable by agent
    Given two agents each with a pending approval
    When agent A's interactions are listed
    Then only A's approval is returned
    And answering it resolves exactly that one
