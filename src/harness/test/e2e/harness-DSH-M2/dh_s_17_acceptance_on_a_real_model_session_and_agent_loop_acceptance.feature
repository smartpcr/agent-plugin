@story-harness:DSH-M2 @phase-dh-s-17-acceptance-on-a-real-model @stage-session-and-agent-loop-acceptance @setup-inline
Feature: Session and agent loop acceptance (DH-E-SESSION-1, -2, DH-E-AGENT-1 to -4)
  Stage 7.3 of harness:DSH-M2 (impl.md 17.2 and 17.3) runs the two session
  scenarios and the four agent-loop scenarios on the S17 machine (TC-20s). The
  harness gate runs the single-process scenarios before the two that kill a
  host (stageOrder in tools/harness-gate/scenariorun.go), and each row of
  docs/stories/harness-DSH-M2/evidence/DH-S-17.md keeps the run's commit and
  prompt count (M2-C-5).

  # proof: service:model-route-claude-code. The signed-in claude CLI exists only
  # on the S17 machine; everywhere else the route is an ephemeral in-process
  # instance: a loopback model server registered as route "claude-code" through
  # the testhook build of dsh, driven over the SDK protocol (os-process), with
  # the embedded SQLite store of a scratch home. Every scenario counts the
  # prompts it sends and its record cites that count.

  Background:
    Given dsh built from this checkout with the testhook tag
    And a scratch home whose route "claude-code" is served by an in-process model instance

  Scenario: DH-E-SESSION-1 a session rebuilds from its log
    Given an sdk host on the scratch home with the file tool "read" over a workspace holding "notes.txt"
    When a session runs three turns, the first reading "notes.txt" with "read"
    And the host is stopped and a fresh host reads the session back from the store
    Then the history read back equals the history before the stop, event for event
    And every request rebuilt from the persisted log has the digest the route received, one per settlement in log order
    And rebuilding the requests twice gives the same bytes
    And the persisted log runs seq 0 to n with no gap
    And the record of "DH-E-SESSION-1" cites a prompt count of 3, equal to the user messages in its persisted log

  Scenario: DH-E-SESSION-2 a host killed during a tool call resumes the session
    Given an sdk host on the scratch home with the long tool "testkit_sleep"
    When a session's turn calls "testkit_sleep" and the tool/call is durable while the tool runs
    And the host is killed without a graceful stop
    And a new host on the same home resumes the session
    Then session/resume reports that it closed an interrupted turn
    And the history after the resume begins with the history before the kill, event for event
    And the resume appended exactly tool/result, step/end, turn/end and session/end-seed, the result outcome-unknown for the call and the turn interrupted
    And a second resume on another new host appends no closer
    And the next turn completes with an assistant/message
    And the persisted log runs seq 0 to n with no gap
    And the record of "DH-E-SESSION-2" cites a prompt count of 2, equal to the user messages in its persisted log

  Scenario: DH-E-AGENT-1 a multi-step turn is bracketed and replayable
    Given an sdk host on the scratch home with the file tool "read" over a workspace holding "notes.txt"
    When one prompt that needs "notes.txt" runs to idle
    Then the log holds one turn/start and one turn/end completed
    And the turn has at least 2 steps, each step/start closed by its step/end in order
    And every tool/call has its tool/result and every step settles exactly once
    And every request rebuilt from the persisted log has the digest the route received, one per settlement in log order
    And the record of "DH-E-AGENT-1" cites a prompt count of 1, equal to the user messages in its persisted log

  # The sdk host does not send session.stream yet (wire/sdkserver/forward.go):
  # the chunk "shown" is the one the route flushed before the cancel.
  Scenario: DH-E-AGENT-2 cancelling a stream keeps what was shown
    Given an sdk host on the scratch home with the stream capability
    When a prompt's answer sends its first chunk and the client cancels the turn keeping the inbox
    And "repeat your last sentence" is sent after the session is idle
    Then the first turn ends aborted with cause user
    And its one settlement is an assistant/message holding the sent text and no tool call
    And dsh cut off the first turn's response and no text sent after the cancel is in the log
    And the request of the second turn rebuilt from the persisted log carries the text shown before the cancel
    And the record of "DH-E-AGENT-2" cites a prompt count of 2, equal to the user messages in its persisted log

  Scenario: DH-E-AGENT-3 stopping and killing the host leave an honest log
    Given an sdk host on the scratch home with the long tool "testkit_sleep"
    When run A stops the host gracefully while "testkit_sleep" runs
    Then before any resume the persisted log of run A holds the call's tool/result, step/end and turn/end aborted with cause disposed
    And the persisted log of run A runs seq 0 to n with no gap
    When run B kills a fresh host on a fresh home while "testkit_sleep" runs, and a new host resumes the session
    Then the resume of run B appends an outcome-unknown tool/result and the closers, and the turn is not continued
    And the persisted log of run B runs seq 0 to n with no gap
    And the record of "DH-E-AGENT-3" cites a prompt count of 2, equal to the user messages in its persisted logs

  # The real scenario's layer sets "budget.maxSteps: 2", which the product cannot
  # take yet: agent.driver builds its factory with no budget, so every turn runs
  # under driver.DefaultBudget. The built binary is held to that budget here, the
  # same stop on a larger number; the maxSteps-2 layer stays owed to the S17 run.
  Scenario: DH-E-AGENT-4 the step budget stops a tool loop
    Given an sdk host on the scratch home with the file tool "read" over a workspace holding "1.txt", "2.txt", "3.txt" and "4.txt"
    When a prompt whose every step reads one of "1.txt", "2.txt" and "3.txt" runs to idle
    And "continue" is sent and its turn reads "4.txt" before it answers
    Then the first turn ends budget with limit steps at the default step budget, after exactly that many steps
    And every tool/call of either turn has its tool/result, each a read returning the file's content
    And the second turn reads a file the first turn did not
    And the record of "DH-E-AGENT-4" cites a prompt count of 2, equal to the user messages in its persisted log

  # The impl diff: the gate's run order of DH-S-17 (stageOrder, phases).
  Scenario: The gate runs the host-killing scenarios after the single-process ones
    Given the committed test/harness/acceptance-index.yaml
    When "harness-gate require --stage DH-S-17" lists the stage's scenarios
    Then it lists "DH-E-SESSION-1", "DH-E-SESSION-2", "DH-E-AGENT-1", "DH-E-AGENT-2", "DH-E-AGENT-3" and "DH-E-AGENT-4"
    And the gate's stageOrder of "DH-S-17" holds "DH-E-SESSION-2" and "DH-E-AGENT-3" in its Late phase, after the single-process scenarios
    And the harness-gate phase test "TestPhasesOrderDHS17" passes

  # M2-C-5; proof: golden (the committed record at HEAD).
  Scenario: The six evidence rows are passed with a prompt count, or owed
    Given the committed record "docs/stories/harness-DSH-M2/evidence/DH-S-17.md" still reading "Closure status: open"
    When the required rows of "DH-E-SESSION-1", "DH-E-SESSION-2", "DH-E-AGENT-1", "DH-E-AGENT-2", "DH-E-AGENT-3" and "DH-E-AGENT-4" are read
    Then each row reads passed with a commit SHA and artifacts citing its prompt count, or reads "not run (<reason>)" with no commit and no artifacts
    And while any of them is not run, the "Stage 7.3 blockers" section names what clears each blocker and that each row records the prompt count (M2-C-5)
