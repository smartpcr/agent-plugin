@story-harness:DSH-M2 @phase-dh-s-15-listener-sign-in-stdio-and-sdk-server @stage-listener-and-embedded-assets @setup-inline
Feature: Listener and embedded assets
  host.listen is the one HTTP listener of the host. It is loaded as a cordis
  component on a real loopback socket (127.0.0.1:0), and every request runs the
  admission pipeline: the mount is chosen by longest prefix, then the
  listen/authenticate bail key (answered here by an admit-all or admit-none
  fake) admits the request, and only then does the mount's handler run. Over
  TLS the certificate pair is re-read from disk when the files change, so a new
  connection presents a replaced pair without a restart.

  # TC-10b
  Scenario: Longest-prefix mount after admission
    Given host.listen is loaded on "127.0.0.1:0" with an admit-all authenticate fake
    And handlers are mounted at "/api" and "/api/sdk"
    When "GET /api/sdk/x" is sent
    Then the response status is 200
    And the response was answered by the "/api/sdk" handler for peer "p1"
    And the "/api" handler ran 0 times
    When "GET /api/other" is sent
    Then the response status is 200
    And the response was answered by the "/api" handler for peer "p1"
    When "GET /apix" is sent
    Then the response status is 404
    And the "/api" handler ran 1 times
    And the "/api/sdk" handler ran 1 times

  # TC-10b
  Scenario: Longest-prefix mount refused before any handler runs
    Given host.listen is loaded on "127.0.0.1:0" with an admit-none authenticate fake
    And handlers are mounted at "/api" and "/api/sdk"
    When "GET /api/sdk/x" is sent
    Then the response status is 401
    When "GET /api/other" is sent
    Then the response status is 401
    And the "/api/sdk" handler ran 0 times
    And the "/api" handler ran 0 times

  # TC-10b
  Scenario: TLS reload
    Given a per-test certificate pair with serial 1 written to a temp directory
    And host.listen is loaded on "127.0.0.1:0" over TLS from those files with an admit-all authenticate fake
    And handlers are mounted at "/api" and "/api/sdk"
    When a new TLS connection is opened
    Then the presented certificate has serial 1
    When the files are replaced with a new pair with serial 2
    And a new TLS connection is opened
    Then the presented certificate has serial 2
    And the listener origin is unchanged
    When "GET /api/sdk/x" is sent over HTTPS
    Then the response status is 200
    And the response was answered by the "/api/sdk" handler for peer "p1"
