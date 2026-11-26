@story-harness:DSH-M2 @phase-dh-s-15-listener-sign-in-stdio-and-sdk-server @stage-declared-effect-route-table @setup-inline
Feature: Declared-effect route table
  host.web is the route table of the UI. Every route is a declaration with a
  name, a method and pattern, the effect it has on state, and the path
  parameters its handler may read. Routes.Handle registers all routes of one
  call or none: a route without an effect declaration is refused and the table
  is left as it was. The routes are an effect of the registering component, so
  disposing the component removes them, cancels every handler still serving
  one of them and waits for it to return; a later request is 404.

  # TC-10b (logic)
  Scenario: Undeclared route refused
    Given an empty host.web route table
    And a component has registered "session.show" as "GET /sessions/{sid}" with effect "read"
    When a component registers "home" as "GET /" with effect "read" and "session.prompt" as "POST /sessions/{sid}/prompt" with no effect
    Then the registration is refused with reason "web/undeclared-effect"
    And the route table is unchanged
    And the route table lists only "session.show"
    When a component registers "session.prompt" as "POST /sessions/{sid}/prompt" with no effect
    Then the registration is refused with reason "web/undeclared-effect"
    And the route table is unchanged

  # TC-10b
  Scenario: Cleanup cancels in-flight
    Given host.listen is loaded on "127.0.0.1:0" with an admit-all authenticate fake and host.web
    And a component has registered the slow route "slow.show" as "GET /slow/{id}" with effect "read"
    When "GET /slow/7" is sent and the slow handler has started
    And the component is disposed
    Then the slow handler's context was cancelled because its owner unloaded
    And the dispose returned only after the slow handler did
    And the slow handler saw path value "id" = "7" for peer "p1"
    When "GET /slow/7" is sent
    Then the response status is 404
    And the route table is empty
