@story-harness:DSH @phase-tool-pipeline-approvals-and-human-commands @stage-durable-inbox-questions-commands-and-grants @setup-compose
Feature: Durable inbox, questions, commands and grants
  An agent's input is a logged inbox: a sent message is durable before Send
  returns, and an accepted message runs as at most one turn, also across a
  crash and a resume. A session grant lives with its agent incarnation and never
  survives a resume. A question the model asks with wait is answered through the
  inbox: the waiting call returns the answer and both are logged with who
  answered.

  # TC-10b [proof: service:sqlite; deps: sqlite embedded; filesystem via t.TempDir()]
  Scenario: Inbox item durable before Send returns
    Given the SQLite store in a temp home with session "s-inbox" and its inbox
    When Send returns for message "m1" with text "hello" on the next-turn list
    Then a second connection to the database immediately reads the inbox splice of "m1" at the receipt's seq

  # DH-T-AGENT-6, TC-10a [proof: in-process; deps: none]
  Scenario: One accepted message is at most one turn
    Given session "s-crash" over the memory store with message "m1" in its inbox
    And a driver stub that takes "m1" as turn 1 and crashes before the turn ends
    When the agent resumes the session
    Then the resume reports the open turn as interrupted
    And the resumed inbox has nothing pending and a claim of turn 2 takes nothing
    And the log shows 1 turn and 1 user message for "m1"
    And a resend of "m1" is refused as a duplicate with the first receipt

  # DH-D-HUMAN-3 [proof: in-process; deps: none]
  Scenario: Grant does not survive resume
    Given an agent of session "s-grant" answered on channel "ui" with allow-session then allow-once
    And a call needing directory "w" is approved and creates a session grant of "w"
    When the session is ended and resumed by a new incarnation
    Then the grant list of the resumed agent is empty
    And a call needing directory "w" asks again
    And the session log holds 1 grant/created and 1 grant/ended with reason "session-ended"

  # TC-10a [proof: in-process; deps: none]
  Scenario: Question answered through the inbox
    Given the model of session "s-ask" called "ask_user_question" as "c1" with arguments '{"question":"Colour?","wait":true}'
    And a fake answerer on channel "ui" that replies "blue"
    When the call runs
    Then the waiting call returns status "answered" with answer "blue" by "testkit"
    And the reply entered the inbox as a "question-answer" message by "testkit" naming call "c1" and was handed to the call
    And the log holds question/asked for call "c1" and question/answered "blue" attributed to "testkit" on channel "ui"
