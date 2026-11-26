@story-harness:DSH-M2 @phase-dh-s-13-sandbox-launch-linux-backends-and-shell-tools @stage-workspace-instruction-files @setup-inline
Feature: Workspace instruction files
  The instructions component discovers AGENTS.md / CLAUDE.md from the project
  root down to the working directory through the fenced filesystem seam,
  renders them as one framed section within a byte budget, and reconciles the
  section when a file changes (context-compaction-skill.md sec. 6.2, C41).

  # TC-07a [proof: in-process; deps: none -- fake seam]
  Scenario: Outside link refused
    Given a fake seam with "/outside/secret.md" holding "TOP SECRET guidance"
    And the fake seam file "/ws/sub/AGENTS.md" holding "sub rules"
    And "/ws/AGENTS.md" in the fake seam is a link to "/outside/secret.md"
    When discovery runs from "/ws/sub" under the project root "/ws"
    Then exactly one candidate is refused
    And "AGENTS.md" is refused with "fs/sandbox-denied"
    And no byte of "/outside/secret.md" was opened
    When the section renders with the default budget
    Then the section does not contain "TOP SECRET"
    And the section contains "sub rules"
    And the section record tracks no file "AGENTS.md"
    And the section record tracks "sub/AGENTS.md" as "visible"
    And the section record carries the refusal of "AGENTS.md" with "fs/sandbox-denied"

  # TC-07g [proof: golden; deps: committed section.golden]
  Scenario: Rendered section
    Given a fake seam holding the three nested instruction files of the section golden
    When discovery runs from "/ws/a/b" under the project root "/ws"
    And the section renders with a budget of 700 bytes
    Then the section is byte-identical to the committed "section.golden"
    And the section is at most 700 bytes
    And the section record tracks "AGENTS.md" as "omitted"
    And the section record tracks "a/AGENTS.md" as "omitted"
    And the section record tracks "a/CLAUDE.md" as "duplicate"
    And the section record tracks "a/b/AGENTS.md" as "visible"
    And "a/b/AGENTS.md" is marked truncated by the budget
    And the frame tags inside "a/b/AGENTS.md" are escaped
    And rendering again yields the same text and digest

  # TC-07a watch part [proof: service:filesystem; deps: t.TempDir() and util/watch]
  Scenario: Edit is reconciled
    Given a real temporary workspace whose "AGENTS.md" holds "use tabs"
    And a reconciler on the local seam watched with a 50 ms debounce
    When the first section is reconciled
    Then the section is a "baseline" that contains "use tabs"
    When "AGENTS.md" is rewritten to hold "use spaces"
    Then the watch notices the rewrite within 10 seconds
    When the next section is reconciled against the one the model saw
    Then the section is an "update" that contains "use spaces"
    And the section does not contain "use tabs"
    And the instructions.visible fold digest changed to the new section's digest
