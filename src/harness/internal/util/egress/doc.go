// Deferred tests (decision M1-D-CLOSE-5; impl.md's DH-S-13 section names them).
// The package doc is in egress.go. Step 6.16 is built with H-30 held open in the
// DH-S-6 evidence record: the child-environment proxy overlay egress.ChildEnv
// (DH-T-UNDICI-9, gap-closure plan step 19.3.7), proven in DH-S-13 with the
// managed environment of the shell executors, and the dial guard against private
// addresses (DH-T-UNDICI-7, step 19.3.8), kept with the rest of plan stage 19.3,
// which must precede DH-S-13.
//
//dsh:test DH-T-UNDICI-7 later DH-S-13
//dsh:test DH-T-UNDICI-9 later DH-S-13

package egress
