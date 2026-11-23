// Package scenarios holds the real-environment scenarios (DH-E-*) of the harness:
// *_scenario_test.go files with //go:build scenario, each test marked with the
// //dsh:scenario directive naming its ID and listed under its executing stage in
// test/harness/acceptance-index.yaml. They run only from the credentialed workflow
// .github/workflows/harness-scenarios.yml, at the stage impl.md assigns (EV-4); the
// keyless gate never compiles them into a test run.
//
// Deferred tests (impl.md's DH-S-1 "Scenarios" paragraph and the DH-S-16 exit
// evidence name each one). Step 1.6 fixed the test layers, but the layer-tag check
// over test/replay and test/scenario (DH-T-TOOLCHAIN-1), the fuzz targets of the
// listed boundary parsers with a committed crasher (DH-T-TOOLCHAIN-3) and the
// byte-identical replay of one fixture (DH-T-TOOLCHAIN-5) need the replay and
// scenario helpers, so they pass in DH-S-16.
//
//dsh:test DH-T-TOOLCHAIN-1 later DH-S-16
//dsh:test DH-T-TOOLCHAIN-3 later DH-S-16
//dsh:test DH-T-TOOLCHAIN-5 later DH-S-16
package scenarios
