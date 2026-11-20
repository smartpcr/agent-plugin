@story-harness:DSH @phase-session-core-contracts @stage-live-session-history-and-brackets @setup-inline
Feature: Live session history and brackets
  A live session over the in-memory reference store delivers every committed
  event once and in order, rebuilds provider requests byte-identically from its
  log, and refuses appends that would unbalance its turn/step brackets.

  Scenario: Ordered once-only delivery under race
    Given a live session on memstore and 3 subscribers
    When 1000 events are appended from 8 goroutines
    Then every append got a distinct dense sequence number with non-decreasing times
    And each subscriber receives every event exactly once in sequence order

  Scenario: Rebuilt request equals digest
    Given a live session on memstore with a request header for route "r/a" and tool "search"
    And a hand-written sequence of a user message, an assistant tool call "c1" and its tool result
    When NextRequest is built
    And more events are appended after the request
    And surface.Rebuild is called over the log prefix ending at the request's requestAsOf
    Then the rebuilt canonical bytes and Digest equal the live request's
    When a builder event is committed and RequestFrom is called on its seq
    Then the auxiliary request's Meta names that event and its purpose "title"
    And RequestFrom on the same seq after later appends yields the same digest

  Scenario: Unbalanced bracket refused on append
    Given a live session on memstore with an open step/start inside turn/start
    When turn/end is appended before step/end
    Then the append is refused with a bracket error
    And the log is unchanged
    When the failure path appends step/end then turn/end
    Then both appends succeed and no bracket is left open
    And for 50 random bracket nestings every premature turn/end is refused and the log is unchanged
