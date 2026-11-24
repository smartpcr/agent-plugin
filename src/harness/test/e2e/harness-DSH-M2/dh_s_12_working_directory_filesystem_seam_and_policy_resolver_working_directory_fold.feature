@story-harness:DSH-M2 @phase-dh-s-12-working-directory-filesystem-seam-and-policy-resolver @stage-working-directory-fold @setup-inline
Feature: Working directory fold
  The workingDirectory fold (C31) is rebuilt from the session log, the host's
  changes of one session serialize, and a directory deleted under a session is
  reported to the next tool call as workdir/unavailable with exactly one inbox
  notice.

  # TC-01a; proof: in-process; deps: none -- memstore and fake seam
  Scenario: Fold rebuilt from the log
    Given an in-memory event log with two workingDirectory changes
    When the views are rebuilt
    Then workdir.Access.Path() equals the second path
    And the fold's workspace and root match the second path
    And the rebuilt fold is at revision 2 and available
    And replaying the log through the fold definition gives the same record

  # TC-01a; proof: in-process; deps: none
  Scenario: Concurrent changes serialize
    Given two goroutines calling Change on one session inside a synctest bubble
    When both complete
    Then the log holds two ordered events
    And each change is immediately followed by its own context line
    And the final fold equals the later one
    And one moved notice was queued per change in log order

  # TC-01b; proof: service:filesystem; deps: filesystem via t.TempDir()
  Scenario: Deleted directory reported
    Given a session whose working directory is a temp subdirectory that the test then removes
    When the next tool call asks workdir.Access
    Then it returns "workdir/unavailable"
    And exactly one inbox notice is queued
    And the queued notice is an unavailable notice for the removed directory
    When the next tool call asks workdir.Access again
    Then it returns "workdir/unavailable"
    And exactly one inbox notice is queued
