// Package scenarios holds the real-environment scenarios (DH-E-*) of the harness:
// *_scenario_test.go files with //go:build scenario, each test marked with the
// //dsh:scenario directive naming its ID and listed under its executing stage in
// test/harness/acceptance-index.yaml. They run only from the credentialed workflow
// .github/workflows/harness-scenarios.yml, at the stage impl.md assigns (EV-4); the
// keyless gate never compiles them into a test run.
//
// Deferred tests (impl.md's DH-S-1 "Scenarios" paragraph and the DH-S-16 exit
// evidence name each one). Step 1.6 fixed the test layers; step 16.7 built the
// replay and scenario helpers, so the layer-tag check (DH-T-TOOLCHAIN-1) and the
// byte-identical replay of a fixture (DH-T-TOOLCHAIN-5) now run in the
// harness-replay gate (src/harness/test/replay), and a required scenario that
// skips fails (DH-T-TOOLCHAIN-6, testkit/scenario). The fuzz targets of the
// listed boundary parsers with a committed crasher (DH-T-TOOLCHAIN-3) need the
// boundary list of DH-D-UTIL-8's fuzz half, which a later step of DH-S-16 builds.
//
//dsh:test DH-T-TOOLCHAIN-3 later DH-S-16
package scenarios
