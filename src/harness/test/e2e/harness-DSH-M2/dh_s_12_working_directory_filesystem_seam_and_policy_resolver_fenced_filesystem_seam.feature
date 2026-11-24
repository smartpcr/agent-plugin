@story-harness:DSH-M2 @phase-dh-s-12-working-directory-filesystem-seam-and-policy-resolver @stage-fenced-filesystem-seam @setup-inline
Feature: Fenced filesystem seam on the real provider (fs/local)
  The fs/local provider passes the fs conformance suite, keeps every write
  inside its write root even while a directory is swapped for a link to an
  outside directory, and refuses a write guarded by a stale version (TC-01b).

  Scenario: Conformance on the real provider
    Given "fs/local" rooted at "t.TempDir()"
    When "fstest.Run" executes on this lane
    Then every subtest passes

  Scenario: Fence holds through a swapped link
    Given a write root "R" and an outside directory "O" in "t.TempDir()"
    When a helper goroutine swaps "R/sub" for a link to "O" 1000 times while the seam writes "R/sub/f"
    Then no file ever appears under "O"
    And each refused write returns "fs/sandbox-denied"
    And the link kind used for the swap is recorded

  Scenario: Stale version refused
    Given file "f" read at version v1 and then rewritten to v2 by another handle
    When a write with v1 is attempted
    Then it returns "fs/stale-version"
    And the file still holds v2 bytes
