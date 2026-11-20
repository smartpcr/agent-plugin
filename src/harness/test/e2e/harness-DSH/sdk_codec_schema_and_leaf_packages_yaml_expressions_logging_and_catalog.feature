@story-harness:DSH @phase-sdk-codec-schema-and-leaf-packages @stage-yaml-expressions-logging-and-catalog @setup-inline
Feature: YAML, expressions, logging and catalog
  The loader reads YAML and JSON to the same entries, yamlx edits keep comments,
  the expression engine is pure, and the compiled-in catalogue lists its components.

  Scenario: Same entries through JSON and YAML codecs
    Given equivalent in-memory JSON and YAML entry documents
    When each document is loaded through its codec
    Then the effective entry trees are equal
    And the YAML document reports no issues
    And the entry "grp" is positioned at "app.json:3:3" in JSON and "app.yaml:11:3" in YAML
    And the YAML expression node for "port" points at the "!expr" tag in the YAML source
    And the JSON expression node for "port" points at its "{" in the JSON source

  Scenario: yamlx edit keeps comments
    Given an in-memory YAML document with comments
    When yamlx sets the scalar at "0/config/host" to "0.0.0.0"
    Then the edited output equals the committed golden "internal/boot/yamlx/testdata/edit_keep_comments.golden"
    And every comment of the source document is retained in the output
    And the parsed source document still holds "localhost" at "0/config/host"

  Scenario: Expression engine has no side effects
    Given an expression evaluator over an in-memory scope with env "EDITOR=vim" and launch profile "dev"
    When the expression "env.EDITOR" is evaluated
    Then it resolves to "vim"
    When the expression "launch.profile" is evaluated
    Then it resolves to "dev"
    When the expression "undefinedName" is evaluated
    Then it fails with an error naming "undefinedName"
    And no enabled builtin reaches the filesystem, network, processes or clock
    And calling any of "readFile, writeFile, exec, getenv, http, open, now, date" is rejected

  Scenario: Catalog lists components by name
    Given the compiled-in catalog
    When it is printed
    Then the output equals the committed golden "internal/boot/catalog/testdata/catalog.golden"
    And every listed component resolves by its name
