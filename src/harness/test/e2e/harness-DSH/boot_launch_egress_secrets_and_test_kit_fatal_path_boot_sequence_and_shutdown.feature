@story-harness:DSH @phase-boot-launch-egress-secrets-and-test-kit @stage-fatal-path-boot-sequence-and-shutdown @setup-inline
Feature: Fatal path, boot sequence and shutdown
  Boot runs its steps in the documented order, fails a broken required entry
  once with one report, and bounds shutdown, killing a stubborn tree with 124.

  Scenario: Broken required entry fails boot once (TC-06b)
    Given a temp home and a layer with a broken required entry "probe" named "x.probe"
    When boot runs
    Then boot exits with the configuration status 78
    And exactly one fatal message is printed naming the entry "probe"
    And exactly one file exists in "<home>/reports/" naming the entry "probe" and "x.probe"
    And the fatal message names the report file

  Scenario: Boot order and audit on fakes (TC-06a)
    Given in-memory fakes for every boot step with no permit configured
    When boot runs until ready and is stopped with status 0
    Then the steps were called in the order "facts, home, guards, layers, compose, tree, settle, audit"
    And readiness came only after the audit
    And the tree was unloaded after the audit
    And the audit lists zero permits
    And boot exits with status 0

  Scenario: Shutdown overrun exits 124 (TC-06c)
    Given a composed tree whose child process ignores termination
    And a shutdown budget shortened by the test kit's manual clock
    When boot is ready and shutdown runs past its bound
    Then boot exits with status 124
    And the process tree is killed and no child remains
    And the stuck cleanup is listed on the diagnostic stream
