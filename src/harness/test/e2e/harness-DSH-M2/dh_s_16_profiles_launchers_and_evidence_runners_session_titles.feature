@story-harness:DSH-M2 @phase-dh-s-16-profiles-launchers-and-evidence-runners @stage-session-titles @setup-inline
Feature: Session titles (TC-18a, DH-T-VIEW-10)
  Titles (session.title, C56) follow every session the session service
  creates. Before any generation the title fold shows a fallback cut from the
  first prompt; after the first turn ends, one title request is logged, sent on
  the session's route and settled by one attempt, and an accepted reply becomes
  the generated title. A renamed title is pinned: Refresh refuses it with
  title/pinned and leaves it unchanged until Unpin.

  Everything runs in-process: a memstore session service, the llm runtime with
  the keyless replay route (llm.replay, C54) playing the recorded title replies,
  and the title service bound to the runtime through llm/titlemodel.

  Scenario: Fallback then generated
    Given a title service whose replay route records the title reply "Fix the login bug"
    And a session whose first turn opens with the prompt "please fix the login bug in the auth form"
    Then the title fold shows the fallback "please fix the login bug"
    And the session log holds no title events
    When the first turn completes
    Then the title fold shows the generated title "Fix the login bug"
    And the session log holds the title events "session/title-request,session/title-attempt,session/title" in order
    And the attempt settled the request as accepted, served by the replay route
    And the title event names the request and the prompt it covered
    And the request sent to the model is the one rebuilt from the logged request event

  Scenario: Pinned title not overwritten
    Given a title service whose replay route records the title reply "Login bug fix"
    And a session whose first turn opens with the prompt "please fix the login bug"
    And the session is renamed to "  My    session  "
    Then the title fold shows the pinned user title "My session"
    When Refresh runs on the session
    Then Refresh is refused as title/pinned
    And the title fold still shows the pinned user title "My session" at the same revision
    And no title request was logged
    When the title is unpinned
    Then the title fold shows the unpinned user title "My session"
    When Refresh runs on the session
    Then the title fold shows the generated title "Login bug fix"
