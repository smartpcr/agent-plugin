@story-harness:DSH-M2 @phase-dh-s-14-agent-presets-and-bundles @stage-shipped-presets-and-persona @setup-inline
Feature: Shipped presets and persona
  The binary ships the standard agent preset (standard.yml): the coding agent
  with the six file tools and the two one-shot shell tools. agent.persona is a
  child of a preset declaration only; enabled at the top level of a config it
  is refused at boot.

  # TC-09b [proof: in-process; deps: none]
  Scenario: Persona outside a preset refused
    Given a config layer that enables "agent.persona" at top level as entry "persona"
    When boot loads it
    Then the boot fails with the configuration status and one fatal message
    And the fatal message names "persona.name" and "agent.persona"
    And the fatal message says a persona mounts only as a child of an "agent.preset"

  # TC-09b logic [proof: in-process; deps: none -- fake tool registrations]
  Scenario: Standard preset declaration
    Given the committed shipped preset "standard"
    And a test registry with fake tool registrations for "read write edit apply_patch glob grep bash pwsh"
    When the preset registry resolves "standard"
    Then the declaration has no issues against the test catalogue
    And an agent bound to it has exactly the tools "read write edit apply_patch glob grep bash pwsh"
    And the agent's persona prefix starts with "You are a coding agent."
    And an agent bound to no preset has no tools
    When a fixture preset "fx" declares children "tool.nope" and "fs.tools"
    Then the fixture is refused naming "tool.nope"
    And the refusal does not name "fs.tools"

  # TC-09b [proof: service:sqlite; deps: embedded modernc SQLite in t.TempDir()]
  Scenario: Standard preset composes an agent with the real tools
    Given the committed shipped preset "standard"
    And the real "fs.tools" and "shellexec.tools" components registered by their constructors
    And an embedded SQLite session store
    When an agent is created bound to preset "standard"
    Then the agent's tool list is exactly "read write edit apply_patch glob grep bash pwsh"
    And the agent's tools are the real constructors' declarations
    And the registry reports the agent bound to "standard" rev 1
    And the session's preset fold reads "standard" by create
    And after reopening the SQLite store the session's preset fold still reads "standard"
