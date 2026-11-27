@story-harness:DSH-M2 @phase-dh-s-16-profiles-launchers-and-evidence-runners @stage-guarded-scenarios-on-scratch-homes @setup-inline
Feature: Guarded scenarios on scratch homes (DH-S-16 step 16.8)
  The four guarded scenarios of Stage 6.12 run on scratch homes: real dsh
  processes and real store processes share one embedded SQLite store in a
  temporary home, are killed without a clean stop, and the home is inspected
  row by row and file by file afterwards. Model turns are played by the keyless
  replay route of the testhook build, so no key or sign-in is read.

  @TC-19s1 @DH-E-STORE-1 @proof:service:sqlite
  Scenario: DH-E-STORE-1 two headless hosts on one home have one writer
    Given dsh built from this checkout with the keyless replay route
    And a scratch home with its embedded SQLite store
    When headless host A creates a session and runs a long turn
    And headless host B resumes A's live session
    Then B is refused "session/owned" naming A's pid
    And the refused resume appended nothing to the session log
    When A is killed without a clean stop and B resumes the session with a long turn
    Then B holds the session with lease epoch one past A's
    And the turn cut by the kill ended "interrupted"
    And A's lock file and hosts row are gone
    When a new copy of A resumes the session
    Then it is refused "session/owned" naming B's pid
    When B is killed and a last headless host resumes and prompts the session
    Then the last run completes on the session
    And the session's seq runs 0..n with no gap

  @TC-19s1 @DH-E-STORE-6 @proof:service:sqlite
  Scenario: DH-E-STORE-6 deletion leaves nothing behind
    Given a scratch home holding session S whose tool results spilled two blobs, and a fork F of S that cites the first
    When a separate process deletes S
    Then S has no row and its unshared blob is gone
    And F and the blob it shares with S survive
    Given a 10000-event session L with a spill file in the scratch home
    When a process deleting L in chunks of 100 events is killed after its first chunk
    Then L is tombstoned with its remaining events still stored
    When a new process opens the store
    Then no row, blob reference or spill file of S or L remains
    When a separate process deletes F
    Then the scratch home holds no session, event, blob reference or spill file
    And the store's integrity check reports clean

  @TC-19s1 @DH-E-FS-3 @proof:service:git-and-editor @proof:service:os-process
  Scenario: DH-E-FS-3 two processes and an editor lose no edit
    Given a scratch workspace file ending in the anchor line and one lock directory both processes share
    And the editor save script (DSH_SCENARIO_FS3_EDITOR, else the suite's own editor save process)
    When two processes each drive 1000 version-guarded edits on the file while the editor saves it
    Then every edit either took effect or returned "fs/stale-version"
    And at least one conflicting edit was refused "fs/stale-version"
    And the final file is unique marker lines then the anchor, holding no refused edit's marker
    And every editor save wrote back a version some process acknowledged
    And every acknowledged edit missing from the final file was overwritten by an overlapping editor save of a version without it
    And the final file equals the last acknowledged version

  @TC-19s2 @DH-E-STORE-2 @proof:service:model-route-claude-code
  Scenario: DH-E-STORE-2 a host killed mid-tool-call recovers honestly
    Given dsh built from this checkout with the keyless replay route
    And a scratch home with its embedded SQLite store
    When a headless host's turn is running a tool call and the host and its children are killed
    And a new headless host resumes the session and sends a new prompt
    Then every event persisted before the kill is unchanged and seq continues with no gap
    And the turn cut by the kill ended "interrupted"
    And the cut tool call's result is "tools/outcome-unknown"
    And the first model request after the resume, read from the replay's request log, carries that result
    When the session is deleted with no host running
    Then no row or spill file of the session remains in the scratch home
    And the guarded runner plans DH-E-STORE-2 on the WSL lane only, behind the Claude Code sign-in, and records the Windows row "out of scope (impl.md 16.8 Linux only; TC-19s2)"
