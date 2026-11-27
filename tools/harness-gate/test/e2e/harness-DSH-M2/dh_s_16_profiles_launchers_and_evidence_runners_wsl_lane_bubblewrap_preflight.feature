@story-harness:DSH-M2 @phase-dh-s-16-profiles-launchers-and-evidence-runners @stage-wsl-lane-bubblewrap-preflight @setup-inline
Feature: WSL lane bubblewrap preflight (implementation-plan Stage 6.10, architecture.md I-8)
  A guarded WSL lane run needs the bubblewrap sandbox backend at state Full.
  The preflight reads a "dsh sandbox status" report and passes only backend
  bwrap, state Full; every other report fails, never skips.

  @proof:in-process @edge
  Scenario: Preflight parses status
    Given recorded "dsh sandbox status" outputs "bwrap Full", "landlock Partial" and "Unconfined"
    When the preflight parser evaluates them
    Then only "bwrap Full" passes
    And "landlock Partial" fails naming backend "landlock" and state "Partial"
    And "Unconfined" fails naming backend "none" and state "Unconfined"
    And the sandbox-preflight command exits 0 for "bwrap Full" and 1 for the others

  @proof:in-process @edge
  Scenario: Preflight rejects a report it cannot read
    Given a "dsh sandbox status" report with no "backend:" line
    When the preflight parser evaluates it
    Then it fails as not a dsh sandbox status report
    And the sandbox-preflight command exits 1 for it

  @proof:service:linux-sandbox-bwrap
  Scenario: Lane preflight on the WSL machine
    Given the WSL lane machine with a clean ext4 checkout of the committed tree
    When "lanes/wsl.sh --guarded-preflight" runs
    Then it exits 0
    And it prints the "dsh sandbox status" report with backend "bwrap" and state "Full"
    And it prints "guarded preflight passed" with bwrap on an absolute path, backend "bwrap", state "Full"
