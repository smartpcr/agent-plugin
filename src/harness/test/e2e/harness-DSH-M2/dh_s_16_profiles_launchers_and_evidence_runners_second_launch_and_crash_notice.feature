@story-harness:DSH-M2 @phase-dh-s-16-profiles-launchers-and-evidence-runners @stage-second-launch-and-crash-notice @setup-inline
Feature: Second launch and crash notice (TC-11b, DH-D-CLI-5)
  A second `dsh web` on the home of a running host does not start another
  host: it calls launch.open on the running one, which opens its browser
  again, and exits 0. A host killed outright (SIGKILL on Linux,
  TerminateProcess on Windows) leaves its runtime record; the next `dsh status`
  names the crash report written for it and suggests --safe.

  The built dsh (./cmd/dsh with the testhook tag, which adds the keyless fake
  route llm.testkit-route and an opener that writes "testkit-opener: opened"
  in place of a browser) runs `dsh web` on a short temp home with a fixed
  free loopback port.

  Scenario: Second web reuses the host
    Given a built "dsh web" running on a temp home
    When a second "dsh web" starts on the same home
    Then the second launch exits 0 after launch.open on the running host
    And the second launch printed no link
    And the running host opened its browser a second time
    And only one listener exists on the home

  Scenario: Crash explained at next start
    Given a built "dsh web" running on a temp home
    And the first host is killed outright
    When "dsh status" runs on the same home
    Then dsh status exits 4 naming the killed host
    And it names a crash report path that exists
    And it suggests --safe
    And "dsh crash" prints the same report path
