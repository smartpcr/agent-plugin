// Deferred tests (decision M1-D-CLOSE-5; impl.md's DH-S-12 section names each
// one). The package doc is in testkit.go. Step 6.17 is built with H-31 held open
// in the DH-S-6 evidence record: the queue half of DH-T-UTIL-12 (with
// conform.ToolCancels), testkit/compose (DH-T-UTIL-13) and testkit/egresstest
// with testkit/faultserver (DH-T-UTIL-15) are built by gap-closure plan stage
// 19.5, which must precede DH-S-12, the first stage whose tests compose real
// components with the kit.
//
//dsh:test DH-T-UTIL-12 later DH-S-12
//dsh:test DH-T-UTIL-13 later DH-S-12
//dsh:test DH-T-UTIL-15 later DH-S-12

package testkit
