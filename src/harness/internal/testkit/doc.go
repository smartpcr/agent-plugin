// Deferred tests (decision M1-D-CLOSE-5; impl.md's DH-S-12 section names each
// one). The package doc is in testkit.go. Step 6.17 is built with H-31 held open
// in the DH-S-6 evidence record. Gap-closure plan stage 19.5 builds what H-31
// holds: steps 19.5.2 and 19.5.3 added testkit/egresstest, testkit/faultserver
// (DH-T-UTIL-15, egresstest_test.go and faultserver_test.go), clock.Run, the
// chunk-level model.Scripted adapter (DH-T-UTIL-14) and the queue half of
// DH-T-UTIL-12 with its conform.ToolCancels row, met by tools/conform
// (queue_test.go). What stays deferred is testkit/compose (DH-T-UTIL-13, step
// 19.5.1), which must precede DH-S-12, the first stage whose tests compose real
// components with the kit.
//
//dsh:test DH-T-UTIL-13 later DH-S-12

package testkit
