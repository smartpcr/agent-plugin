@story-harness:DSH @phase-repository-modules-and-quality-gates @stage-three-module-workspace @setup-inline
Feature: Three module workspace
  The repository holds exactly three product modules (src/go, src/sdk, src/harness),
  joined for development by the root go.work and built for release with GOWORK=off,
  never through a replace directive (RM-1, RM-2, RM-4, DH-ADR-26).

  Scenario: Workspace builds all three modules
    Given a clean checkout with the root go.work
    When "go build ./src/go/... ./src/sdk/... ./src/harness/..." runs from the repository root
    And "go vet ./src/go/... ./src/sdk/... ./src/harness/..." runs from the repository root
    And "GOWORK=off go build ./..." runs in each of the three module directories
    Then every command exits 0
    And "go list -m" reports exactly the three module paths of RM-1

  Scenario: Release build ignores the workspace
    Given GOWORK=off
    When "go build -o <tmp>/dsh ./cmd/dsh" runs in src/harness
    Then the build succeeds without any replace directive
    And the binary prints its version and exits 0

  Scenario: Module pin test rejects a replace directive
    Given an in-memory copy of src/harness/go.mod with the line "replace github.com/smartpcr/agent-plugin/src/go => ../go" added
    When DH-T-TOOLCHAIN-11 checks the parsed bytes
    Then it fails naming the replace line
    And with the committed bytes it passes

  Scenario: cordis-sample golden matches pre-change behaviour
    Given testdata/output.golden committed before the first DH-S-2 edit
    When "go -C src/go test -count=20 ./cmd/cordis-sample" runs without -update
    Then every run's normalized output is byte-identical to the golden
    And the golden holds beat: lines in windows of 2, 2, 2 and 8
    And no "coincident events" failure is raised
    And main still calls run with os.Stdout, os.TempDir(), the console exporter, time.Sleep and a no-op Settle
