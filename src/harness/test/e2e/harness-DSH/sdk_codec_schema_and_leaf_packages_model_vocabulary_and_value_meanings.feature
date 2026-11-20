@story-harness:DSH @phase-sdk-codec-schema-and-leaf-packages @stage-model-vocabulary-and-value-meanings @setup-inline
Feature: Model Vocabulary and Value Meanings
  The model vocabulary (internal/llm/vocab) has one canonical encoding, its
  persisted shapes are pinned by a committed fingerprint golden, it holds no
  request type, and the util glob package gives every pattern one meaning.

  Scenario: Canonical encoding is stable
    Given two Message values built with map fields inserted in different orders
    When each is canonically encoded
    Then the bytes and SHA-256 digests are identical

  Scenario: Vocabulary fingerprints unchanged
    Given the committed fingerprint golden
    When the schema package fingerprints every vocab type
    Then each equals its golden line

  Scenario: vocab holds no request type
    Given the parsed AST of every file in internal/llm/vocab
    When the archtest predicate looks for a type named Request or a struct holding []Message plus Controls
    Then none is found

  Scenario: Glob meaning is single
    Given the glob table:
      | pattern  | name     | match |
      | **/*.go  | a/b/c.go | true  |
      | *.go     | a/b.go   | false |
      | **/*.go  | main.go  | true  |
    When matched with the util glob package
    Then results equal the table
