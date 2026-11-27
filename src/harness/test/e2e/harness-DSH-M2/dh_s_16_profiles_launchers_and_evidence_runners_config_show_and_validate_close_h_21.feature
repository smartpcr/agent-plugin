@story-harness:DSH-M2 @phase-dh-s-16-profiles-launchers-and-evidence-runners @stage-config-show-and-validate-close-h-21 @setup-inline
Feature: Config show and validate close H-21
  `dsh config show` and `dsh config validate` preview a profile's composition
  as a launch would, in the built binary's own process, and mount no tree:
  the component trace is empty and the home is left as it was (TC-13b).

  Scenario: Preview constructs no component
    Given the built dsh binary and a temp home whose user layer sets "logging" level "debug"
    When "dsh config show --profile headless" runs with the component trace on
    Then it exits 0
    And it prints "profile: headless"
    And it prints the value "config.level" as "\"debug\"" from the layer "user"
    And it prints the value "config.destination" as "\"stderr\"" from the layer "bundle:admin"
    And it prints the entry "cli.headless (cli.headless)" from the layer "bundle:headless"
    And the component trace is empty
    And the temp home is unchanged

  Scenario: Validate rejects bad config
    Given the built dsh binary and a temp home whose user layer sets the default permission preset to "ghost"
    When "dsh config validate" runs with the component trace on
    Then it exits with the config status 78
    And stderr names the unresolvable default permission preset "ghost"
    And stdout does not report the configuration valid
    And the component trace is empty
    And the temp home is unchanged
