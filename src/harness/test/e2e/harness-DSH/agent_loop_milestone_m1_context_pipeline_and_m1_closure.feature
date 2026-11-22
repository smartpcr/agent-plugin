@story-harness:DSH @phase-agent-loop-milestone-m1 @stage-context-pipeline-and-m1-closure @setup-compose
Feature: Context pipeline and M1 closure
  The context area's PreStep pipeline enters each contributor's observation as
  a logged user/message in the order table's order, so the request the driver
  builds with NextRequest is rebuilt byte for byte from the log (architecture.md
  sec. 4.2, 5.3). Flow F2 -- one M1 turn with one tool call, one approval and a
  final reply -- serializes to the committed golden on memstore (TC-11a) and, on
  the embedded SQLite store, flushes before each provider attempt, before the
  tool body and at step end, and reopens to the same golden (TC-11b).

  Scenario: Context pipeline is logged and ordered
    Given three context contributors "instructions, skill-catalog, clock" registered in that order
    When the loop request for a step is built with NextRequest
    Then the request's contributions appear in the order "instructions, skill-catalog, clock"
    And each contribution is backed by a logged user/message at or before the request's requestAsOf
    And the request's digest equals the digest of surface.Rebuild over the log prefix ending at requestAsOf
    And the turn ended completed

  Scenario: Flow F2 bracketed log matches golden
    Given the scripted model returning one tool call then a final reply
    And the fake answerer approving
    When one turn runs on memstore
    Then the answerer was asked exactly one approval
    And the F2 brackets are closed and every request sent rebuilds from the log
    And the serialized log equals "internal/agent/testdata/f2_log.golden"

  Scenario: Flow F2 persisted with checkpoints before I/O
    Given the scripted model returning one tool call then a final reply
    And the fake answerer approving
    When one turn runs on the SQLite store in a temp home
    Then the answerer was asked exactly one approval
    And database rows show a flush before the provider attempt, before the tool body and at step end
    And the checkpoints were observed in the order "attempt,body,attempt,end"
    And the F2 brackets are closed and every request sent rebuilds from the log
    And the log reopened from the database equals "internal/agent/testdata/f2_log.golden"
