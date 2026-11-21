@story-harness:DSH @phase-boot-launch-egress-secrets-and-test-kit @stage-egress-policy-and-telemetry-off-switch @setup-inline
Feature: Egress policy and telemetry off switch
  Every outbound connection goes through the egress client, which dials only
  for a holder of a sharing permit; one off-switch denies every kind and the
  boot audit lists each kind as off.

  Scenario: No permit, no dial (TC-06d)
    Given a loopback HTTP server on 127.0.0.1:0 counting connections
    And an egress service and a sharing gate whose "model-request" kind is enabled for the server
    When the egress client is asked to POST to the server without a permit
    Then the request fails with "egress/not-permitted"
    And the server's connection count is 0
    When the egress client is asked to POST to the server with a "model-request" permit
    Then the request arrives at the server with user agent "dsh"
    And the server's connection count is at least 1

  Scenario: Off-switch overrides permits (TC-06a)
    Given a sharing gate with every kind enabled with an endpoint
    And the off-switch is on
    When permit decisions are evaluated in memory
    Then every kind is denied with "sharing/off"
    And the boot audit lists each kind as "off"
    And the boot audit reports the off-switch set and no outbound permits
