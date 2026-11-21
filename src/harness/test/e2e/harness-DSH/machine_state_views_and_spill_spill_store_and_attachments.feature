@story-harness:DSH @phase-machine-state-views-and-spill @stage-spill-store-and-attachments @setup-compose
Feature: Spill store and attachments
  An oversized tool result is saved whole, privately, under <home>/spill so that
  only a bounded head and a spill reference enter the session log; an attached
  file is kept content-addressed under <home>/blobs and read back verified.
  service:filesystem is a temp home (or a fresh directory under
  DSH_E2E_STORE_HOME); service:sqlite is the embedded modernc.org/sqlite engine.

  Scenario: Large result spilled and recoverable
    Given a 5 MB tool result in a session of a temp home
    When it is stored through the spill store with a head bound of 16384 bytes
    Then the logged tool result holds a head of at most 16384 bytes and the spill reference
    And reading the reference returns all 5 MB byte-identical

  Scenario: Corrupted attachment detected
    Given an attachment stored in blobs
    When one byte of the stored file is flipped on disk and it is read back
    Then the read fails with a digest-mismatch error

  Scenario: Spill files are private
    Given a spilled result in a temp home
    When the spill file's permissions are inspected
    Then the file and its spill directories are private to their owner
