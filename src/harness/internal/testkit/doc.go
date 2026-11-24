// Test notes (decision M1-D-CLOSE-5). The package doc is in testkit.go. Step
// 6.17 was built with H-31 held open in the DH-S-6 evidence record; gap-closure
// plan stage 19.5 builds what H-31 held. Steps 19.5.2 and 19.5.3 added
// testkit/egresstest and testkit/faultserver (DH-T-UTIL-15), clock.Run, the
// chunk-level model.Scripted adapter (DH-T-UTIL-14) and the queue half of
// DH-T-UTIL-12 with its conform.ToolCancels row, met by tools/conform
// (queue_test.go). Step 19.5.1 added testkit/compose (DH-T-UTIL-13,
// compose/compose_test.go). No test of this package is deferred.
package testkit
