@story-harness:DSH-M2 @phase-dh-s-15-listener-sign-in-stdio-and-sdk-server @stage-admission-by-source-then-cookie @setup-inline
Feature: Admission by source then cookie (DH-D-HOST-2, TC-10b)
  The listener admits a request by its source, its Host and its Origin before any
  credential is read; host.auth then admits it by a session cookie whose SHA-256 is a
  live row of the SQLite session table. A launch token buys exactly one session.

  Background:
    Given host.listen on "127.0.0.1" with host.auth over a SQLite session table and a Required mount at "/api"

  Scenario: Token is one-time (TC-10b)
    Given a launch token minted by host.auth
    When the launch token is exchanged
    Then the exchange answers 303 to "./" with Cache-Control "no-store"
    And the exchange sets the session cookie HttpOnly, SameSite=Strict, Path=/ and not Secure
    And the SQLite session table holds exactly one row, keyed by the SHA-256 of the cookie and not holding the cookie itself
    When the launch token is exchanged again
    Then the exchange answers 401 with reason "auth/unauthorized" and sets no cookie
    And the SQLite session table still holds exactly one row
    And "GET /api" with the session cookie reaches the mount as the "owner"

  Scenario: Refusals (TC-10b)
    Given a browser signed in with a launch token
    When "GET /api" is sent without a cookie
    Then the response is 401 with reason "auth/unauthorized"
    When "GET /api" is sent with a cookie that matches no session row
    Then the response is 401 with reason "auth/unauthorized"
    When "GET /api" is sent with the session cookie and Origin "http://evil.example"
    Then the response is 403 with reason "auth/origin"
    When "POST /api" is sent with the session cookie and Origin "http://evil.example"
    Then the response is 403 with reason "auth/origin"
    When "GET /api" is sent with the session cookie and Sec-Fetch-Site "cross-site"
    Then the response is 403 with reason "auth/origin"
    And the mount at "/api" was reached 0 times
    Given a second host.listen bound on this machine's non-loopback address with allowed peers "192.0.2.0/24"
    When "GET /api" is sent with the session cookie from this machine's non-loopback address
    Then the response is 403 with reason "auth/source"
    And the mount at "/api" of the second listener was reached 0 times
    Given a third host.listen bound on this machine's non-loopback address that allows that address as a peer
    When "GET /api" is sent to the third listener from this machine's non-loopback address
    Then the response is 401 with reason "auth/unauthorized"
    And the mount at "/api" of the third listener was reached 0 times
    When a launch token minted by the third listener's host.auth is exchanged from this machine's non-loopback address
    Then the third listener's exchange answers 303 to "./" and sets its own session cookie
    When "GET /api" is sent to the third listener with its session cookie from this machine's non-loopback address
    Then the response is 200 with body "owner"
    And the mount at "/api" of the third listener was reached 1 times
