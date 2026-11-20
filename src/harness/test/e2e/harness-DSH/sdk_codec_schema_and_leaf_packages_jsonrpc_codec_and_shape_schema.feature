@story-harness:DSH @phase-sdk-codec-schema-and-leaf-packages @stage-jsonrpc-codec-and-shape-schema @setup-inline
Feature: JSONRPC codec and shape schema
  The src/sdk JSON-RPC codec reads newline-delimited frames from an untrusted peer
  within a line bound, decodes every input to a frame or a table error without
  panicking, and the protocol schema is derived from the Go types and committed as a
  golden. src/sdk depends on nothing of the product (TC-04a, DH-S-4).

  Scenario: Codec rejects an oversized line
    Given an in-memory reader with one line one byte over the configured bound
    When the codec reads
    Then it returns "jsonrpc/line-too-long"
    And it does not allocate past the bound

  Scenario: Embedded fuzz seeds decode without panicking
    Given the seed slice compiled into "src/sdk/jsonrpc/seeds_test.go"
    When TestDecodeSeeds feeds each seed to the decoder in memory
    Then every seed decodes or fails with a table error
    And no seed panics

  Scenario: On-disk fuzz corpus replays as a unit test
    Given the committed files in "src/sdk/jsonrpc/testdata/fuzz/FuzzDecode"
    When "go -C src/sdk test -run FuzzDecode ./jsonrpc" runs without -fuzz
    Then the Go test runner reads every corpus file
    And each corpus input decodes or fails with a table error, none panicking

  Scenario: Derived protocol schema matches golden
    Given the protocol Go types
    When the schema is derived
    Then it is byte-identical to committed "src/sdk/protocol/schema.json"

  Scenario: src/sdk imports nothing of the product
    Given the three modules
    When the archtest lists "src/sdk" dependencies
    Then none has prefix "github.com/smartpcr/agent-plugin/src/go" or "github.com/smartpcr/agent-plugin/src/harness"
