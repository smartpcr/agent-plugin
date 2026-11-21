@story-harness:DSH @phase-machine-state-views-and-spill @stage-typed-machine-state-and-stored-folds @setup-compose
Feature: Typed machine state and stored folds
  Machine state (the workspace registry) lives in dsh.db and survives a
  restart; a stored fold whose version changes is rebuilt from the log when the
  store opens. service:sqlite is the embedded modernc.org/sqlite engine; every
  home is a temp directory (or a fresh directory under DSH_E2E_STORE_HOME).

  Scenario: Machine state survives restart
    Given a workspace registered in a temp home
    When the store is closed and reopened
    Then the workspace row is read back with identical fields

  Scenario: Changed fold version rebuilds
    Given a stored fold at version 1 over 20 events
    When the fold is re-registered at version 2 and the store opens
    Then the stored state is rebuilt, records version 2 and its sequence equals the log head
