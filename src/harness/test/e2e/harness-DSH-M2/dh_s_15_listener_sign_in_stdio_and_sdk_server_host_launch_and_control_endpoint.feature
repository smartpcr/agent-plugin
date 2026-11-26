@story-harness:DSH-M2 @phase-dh-s-15-listener-sign-in-stdio-and-sdk-server @stage-host-launch-and-control-endpoint @setup-inline
Feature: Host launch and control endpoint (DH-D-HOST-3, TC-11b)
  Only a profile that binds a listener loads host.launch beside host.listen and
  host.auth, so only a listening host writes <home>/run/web-<hash>.json. Its control
  endpoint is newline-delimited JSON-RPC on a Unix domain socket in the same
  owner-only directory, and host.status answers with the address and the pid and
  never with a credential.

  Scenario: Only listening hosts write a record (TC-11b)
    Given a temp home
    When a listener profile "web" and a "headless" profile boot in-test
    Then "<home>/run/" holds exactly one runtime record
    And that runtime record is the listener's, with its origin, this process's pid and its control socket
    And that runtime record holds no token, cookie or launch link
    And "<home>/run/" holds no host.launch file but the listener's record, lock and control socket
    When the listener profile is unloaded
    Then "<home>/run/" holds no runtime record

  Scenario: Control endpoint returns no credential (TC-11b)
    Given a temp home
    And a running listener host booted from the "web" profile
    And a launch link minted by that host's host.auth
    When "host.status" is called over the local socket
    Then the reply has the listener's address and this process's pid
    And the reply has no token or cookie field
    And the reply carries neither the launch link's token nor the session cookie's name
