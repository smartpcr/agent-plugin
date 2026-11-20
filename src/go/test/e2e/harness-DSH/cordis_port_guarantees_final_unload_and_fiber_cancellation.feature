@story-harness:DSH @phase-cordis-port-guarantees @stage-final-unload-and-fiber-cancellation @setup-inline
Feature: Final unload and fiber cancellation
  Unloading a cordis fiber is final under concurrency, dependents unload before
  their provider, and a fiber's lifetime is cancelled with the unload cause.

  Scenario: Unload is final under concurrency
    Given a fiber with three effects whose disposers block until released
    When two goroutines call Dispose at once
    Then neither Dispose returns while the disposers are blocked
    When the disposers are released
    Then both Dispose calls return only after all three disposers ran exactly once
    And a later Effect call on the fiber returns the disposed error

  Scenario: Dependents unload before their provider
    Given fiber A providing the service "svc"
    And fiber B injecting the service "svc"
    When fiber A is unloaded
    Then fiber B's disposer observed fiber A's service still live
    And fiber A's disposer ran after fiber B's

  Scenario: Fiber context is cancelled by unload with a cause
    Given a loaded fiber whose effect blocks on the fiber lifetime being done
    When the fiber is unloaded
    Then the blocked effect returns
    And the context cause of the fiber lifetime is the unload cause
    And Wait on the fiber returns after the effect returned

  Scenario: Existing cordis behaviour preserved
    Given the committed cordis-sample golden "cmd/cordis-sample/testdata/output.golden"
    When the existing cordis and cordis-sample test suites run
    Then the existing cordis tests pass
    And the cordis-sample output matches the committed golden
