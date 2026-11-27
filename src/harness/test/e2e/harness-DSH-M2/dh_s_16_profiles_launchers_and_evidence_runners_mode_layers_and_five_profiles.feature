@story-harness:DSH-M2 @phase-dh-s-16-profiles-launchers-and-evidence-runners @stage-mode-layers-and-five-profiles @setup-inline
Feature: Mode layers and five profiles
  The five launchable profiles admin, core, web, headless and sdk each boot
  through boot.Process on a temp home with the keyless fake route, reach ready
  and stop cleanly; only web binds a port. The committed mode layers compose to
  the committed composed.golden.

  # TC-08g plus TC-11b [proof: service:os-process; deps: os-process, filesystem, sqlite (embedded)]
  Scenario: The launchable profiles are exactly the five
    Then the launchable profile definitions are exactly "admin, core, web, headless, sdk"

  # TC-08g plus TC-11b [proof: service:os-process; deps: os-process, filesystem, sqlite (embedded)]
  Scenario Outline: Each profile boots, reaches ready and stops cleanly; only web binds a port
    Given a temp home for the "<profile>" profile with the keyless fake route
    When boot.Process runs the "<profile>" profile in-test
    Then the profile reaches ready with its embedded sqlite session store open
    And the composed tree <listener> a host.listen listener
    And the home <record> a runtime record whose origin accepts a TCP connection
    When the profile's client ends
    Then the profile stops with status 0 and prints no fatal message
    And the home holds no runtime record after the stop

    Examples:
      | profile  | listener     | record        |
      | admin    | mounts no    | holds no      |
      | core     | mounts no    | holds no      |
      | web      | mounts       | holds         |
      | headless | mounts no    | holds no      |
      | sdk      | mounts no    | holds no      |

  # TC-11b: the sdk carrier answers a prompt over its standard streams on the fake route
  Scenario: The sdk profile serves a prompt on the fake route before its client ends
    Given a temp home for the "sdk" profile with the keyless fake route scripted to reply "pong"
    When boot.Process runs the "sdk" profile in-test
    Then the profile reaches ready with its embedded sqlite session store open
    And the sdk client initializes, opens a session on the fake route and its prompt is answered once
    When the profile's client ends
    Then the profile stops with status 0 and prints no fatal message
    And the sdk profile stopped because its input closed
    And the home holds no runtime record after the stop

  # TC-08g [proof: golden; deps: committed composed.golden]
  Scenario: Mode layer composition equals the committed composed.golden
    Given the committed mode layers of every profile definition
    When the layers are composed
    Then every profile composes with no failure and only pending-target issues
    And the composed output equals the committed composed.golden byte for byte
