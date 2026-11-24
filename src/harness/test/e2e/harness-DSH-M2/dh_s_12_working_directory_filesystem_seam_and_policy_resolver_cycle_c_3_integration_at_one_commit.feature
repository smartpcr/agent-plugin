@story-harness:DSH-M2 @phase-dh-s-12-working-directory-filesystem-seam-and-policy-resolver @stage-cycle-c-3-integration-at-one-commit @setup-inline
Feature: Cycle C-3 integration at one commit (TC-01b)
  One process boots the cycle C-3 unit from the build catalogue -- a session
  service over an embedded modernc SQLite store plus fs.local, session.workdir,
  sandbox.policy (workspace-write) and fs.ops -- and the write fence follows the
  session's working directory, across a move and across a resume.

  Background:
    Given an embedded SQLite session store and sibling projects "A" and "B" in a temp directory

  Scenario: Directory move changes the fence
    Given a booted spine with "workspace-write" and working directory "A"
    And a write to "A/x" with "before" succeeds
    And the resolved policy is "workspace-write" with write roots including "A"
    When the host moves the session to "B"
    Then the working directory reported by the spine is "B"
    And the resolved policy is "workspace-write" with write roots including "B" and excluding "A"
    And a write to "A/x" returns "fs/sandbox-denied" and "A/x" still holds "before"
    And a write to "B/x" with "inside" succeeds
    And a relative write lands in "B" and not in "A"

  Scenario: Resume restores the fence
    Given a booted spine with "workspace-write" and working directory "A"
    And a write to "A/x" with "before" succeeds
    And the host moved the session to "B"
    And the process shut down, leaving the session persisted in the SQLite store
    When the process reopens the store and resumes the session
    Then the rebuilt working directory is "B"
    And the resolved policy is "workspace-write" with write roots including "B" and excluding "A"
    And a write to "A/x" returns "fs/sandbox-denied" and "A/x" still holds "before"
    And a write to "B/x" with "inside" succeeds
    And a relative write lands in "B" and not in "A"
