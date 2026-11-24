@story-harness:DSH-M2 @phase-dh-s-12-working-directory-filesystem-seam-and-policy-resolver @stage-six-file-tools @setup-inline
Feature: Six file tools (TC-03b)
  The six file tools (read, write, edit, apply_patch, glob, grep) run through the
  real tool pipeline over fs.local and the operation service, fenced to a
  workspace in a temp directory (workspace-write), with every call logged as a
  tool/result in an embedded SQLite session store and large results saved to a
  local spill store in a temp home.

  Background:
    Given a workspace in a temp directory with the six file tools mounted on the tool pipeline

  Scenario: Tools change files exactly as logged
    Given the workspace file "crlf.txt" holds "one\r\ntwo\r\n"
    When "write" runs with arguments:
      """
      {"file_path": "a.txt", "content": "alpha\nbeta\n"}
      """
    Then the call succeeds
    And "a.txt" on disk is "alpha\nbeta\n"
    And the result and its logged tool/result name "a.txt" at its on-disk version
    When "write" runs with arguments:
      """
      {"file_path": "dir/new.txt", "content": "fresh"}
      """
    Then the call succeeds
    And "dir/new.txt" on disk is "fresh"
    And the result and its logged tool/result name "dir/new.txt" at its on-disk version
    When "edit" runs with arguments:
      """
      {"file_path": "a.txt", "old_string": "beta", "new_string": "gamma"}
      """
    Then the call succeeds
    And "a.txt" on disk is "alpha\ngamma\n"
    And the result and its logged tool/result name "a.txt" at its on-disk version
    When "edit" runs with arguments:
      """
      {"file_path": "crlf.txt", "old_string": "one\ntwo", "new_string": "uno\ndos"}
      """
    Then the call succeeds
    And "crlf.txt" on disk is "uno\r\ndos\r\n"
    And the result and its logged tool/result name "crlf.txt" at its on-disk version
    When "apply_patch" runs with arguments:
      """
      {"input": "*** Begin Patch\n*** Add File: b.txt\n+bee\n*** Update File: a.txt\n@@\n alpha\n-gamma\n+delta\n*** Update File: dir/new.txt\n*** Move to: moved.txt\n@@\n-fresh\n+moved\n*** End Patch"}
      """
    Then the call succeeds
    And "b.txt" on disk is "bee\n"
    And "a.txt" on disk is "alpha\ndelta\n"
    And "moved.txt" on disk is "moved\n"
    And "dir/new.txt" no longer exists on disk
    And the result and its logged tool/result name "b.txt" at its on-disk version
    And the result and its logged tool/result name "a.txt" at its on-disk version
    And the result and its logged tool/result name "moved.txt" at its on-disk version

  Scenario: grep needs no rg
    Given 1000 workspace files under "tree", every tenth holding a needle line
    And PATH is scrubbed to a directory holding only a recording fake rg
    When "grep" runs with arguments:
      """
      {"pattern": "needle \\d+", "path": "tree"}
      """
    Then the call succeeds
    And the result starts with "Found 100 matches\n"
    And the result contains "  Line 3: the needle 990"
    When "grep" runs with arguments:
      """
      {"pattern": "needle", "path": "tree", "output": "count", "include": "f00[0-4]*.txt"}
      """
    Then the call succeeds
    And the result starts with "Found matches in 5 files\n"
    And the fake rg recorded zero spawns

  Scenario: Large result spills
    Given a 5 MiB workspace file "big.txt"
    When "read" runs with arguments:
      """
      {"file_path": "big.txt"}
      """
    Then the call succeeds
    And the numbered lines are truncated at the default read bound of 51200 bytes
    And the result carries a spill reference that the logged tool/result and the session store both cite
    And the spilled content equals the full file
