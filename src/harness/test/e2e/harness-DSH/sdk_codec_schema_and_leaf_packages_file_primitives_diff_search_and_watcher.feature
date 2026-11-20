@story-harness:DSH @phase-sdk-codec-schema-and-leaf-packages @stage-file-primitives-diff-search-and-watcher @setup-inline
Feature: File primitives, diff, search and watcher
  The leaf file packages publish files atomically, round-trip model-format patches,
  honour ignore files when listing, and report one change notice per save.

  Scenario: Atomic replace never exposes a partial file
    Given a file in a temp directory and a reader goroutine reading it in a loop
    When fsx.Replace writes new content 200 times
    Then every read returns either the old or the new full content

  Scenario: Patch round-trip
    Given in-memory texts a and b
    When a model-format patch from a to b is produced and applied to a
    Then the result equals b byte-for-byte

  Scenario: Search honours ignore files
    Given a temp tree with a .gitignore excluding build/
    When search.List runs
    Then no path under build/ is returned
    And the listing equals the committed golden "internal/util/search/testdata/list.golden"

  Scenario: Watcher reports one notice per save
    Given a watch on a temp directory
    When a file is replaced atomically
    Then exactly one change notice for that path arrives within a bounded deadline
