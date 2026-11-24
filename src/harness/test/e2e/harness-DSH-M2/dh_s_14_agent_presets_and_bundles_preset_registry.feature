@story-harness:DSH-M2 @phase-dh-s-14-agent-presets-and-bundles @stage-preset-registry @setup-inline
Feature: Preset registry
  The agent preset registry builds an immutable revision per declaration
  digest; a session leases the revision it bound and keeps it across an edit
  of its preset, while new sessions bind the current revision. Declarations
  whose extends chain forms a cycle are refused at load, naming the cycle.

  # TC-09b [proof: service:sqlite; deps: embedded modernc SQLite in t.TempDir()]
  Scenario: Session keeps its revision
    Given a session bound to preset "standard" rev 1 in an embedded SQLite store
    When the preset "standard" is edited to rev 2
    Then the running session's fold still reads rev 1
    And the running session still sees tool.read "v1"
    And rev 1 of "standard" has 1 lease and is retired
    And rev 2 of "standard" has 0 leases and is current
    And a new session binds rev 2 of "standard"
    And the new session's fold reads rev 2 and sees tool.read "v2"
    And rev 1 of "standard" unloads when the running session's scope closes

  # TC-09b logic [proof: in-process; deps: none]
  Scenario: Extends cycle refused
    Given in-memory declarations "a" extends "b" and "b" extends "a"
    When the registry loads
    Then the load fails with agentpreset/invalid naming the cycle "a -> b -> a"
    And the error names both "a" and "b"
    And the refused load mounted no revision
