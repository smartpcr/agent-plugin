// Package scenarios holds the real-environment scenarios (DH-E-*) of the harness:
// *_scenario_test.go files with //go:build scenario, each test marked with the
// //dsh:scenario directive naming its ID and listed under its executing stage in
// test/harness/acceptance-index.yaml. They run only from the credentialed workflow
// .github/workflows/harness-scenarios.yml, at the stage impl.md assigns (EV-4); the
// keyless gate never compiles them into a test run.
package scenarios
