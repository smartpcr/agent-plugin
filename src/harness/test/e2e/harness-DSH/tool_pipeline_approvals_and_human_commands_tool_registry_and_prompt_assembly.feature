@story-harness:DSH @phase-tool-pipeline-approvals-and-human-commands @stage-tool-registry-and-prompt-assembly @setup-compose
Feature: Tool Registry and Prompt Assembly
  Stage 10.1: the scoped tool registry refuses a second tool of one name in one
  layer and lets a child scope shadow an enclosing one, and the step prompt and
  tool list assemble to the same bytes whatever the registration order.

  # DH-T-TOOLS-2, TC-10a [proof: in-process; deps: none]
  Scenario: Duplicate tool name refused in a scope
    Given a booted tool registry with a parent scope and its child and sibling scopes
    And a scope with tool "echo" described as "First echo."
    When a second "echo" described as "Second echo." is registered in the same scope
    Then registration fails with "tools/duplicate" and returns no handle
    And the parent scope still resolves "echo" to "First echo."
    When "echo" described as "Child echo." is registered in the child scope
    Then the child scope resolves "echo" to "Child echo."
    And the parent scope still resolves "echo" to "First echo."
    And the sibling scope resolves "echo" to "First echo."
    When the child scope's "echo" registration is disposed
    Then the child scope resolves "echo" to "First echo."

  # DH-T-TOOLS-14 [proof: golden; deps: internal/tools/testdata/prompt.golden]
  Scenario: Prompt assembly is deterministic
    Given five tools, five sections, three variables and two runtime contexts to register
    When the step prompt and tool list are assembled 100 times in random registration order
    Then every run produces identical bytes
    And the bytes equal the committed golden "internal/tools/testdata/prompt.golden"
    And the assembled tool list is "read_file,grep,list_dir,write_file,shell" in that order
