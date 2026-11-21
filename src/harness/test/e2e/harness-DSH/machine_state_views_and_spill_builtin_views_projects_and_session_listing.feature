@story-harness:DSH @phase-machine-state-views-and-spill @stage-builtin-views-projects-and-session-listing @setup-compose
Feature: Built-in views, projects and session listing
  The four built-in views (list summary, turn index, statistics, event index)
  are stored folds whose stored rows equal a refold of the log; sessions are
  listed grouped by project; hiding a session stops its agent. service:sqlite is
  the embedded modernc.org/sqlite engine; every home is a temp directory (or a
  fresh directory under DSH_E2E_STORE_HOME). The hidden-session scenario runs on
  memstore with a fake agent.

  Scenario: Refold equals stored for every built-in view
    Given a fixed 200-event session in a temp home
    When each built-in view is refolded from the log and compared with its stored state
    Then all four built-in views are equal

  Scenario: Listing groups by project
    Given three sessions in two projects
    When sessions are listed by project
    Then the result equals the committed golden listing

  Scenario: Hidden session stops working
    Given a session with an agent registered through the agent contract fake
    When the session is hidden
    Then the agent receives a stop with cause "hidden"
