// Deferred tests (decision M1-D-CLOSE-5; impl.md's DH-S-12 section names it).
// The package doc is in views.go. Step 5.13 is built with H-40 held open in the
// DH-S-5 evidence record (gap M1-G-CLOSE-9.3): DH-T-VIEW-4, the import check of
// fold packages (no I/O, clocks or randomness), the fold-twice determinism and
// the slow-fold report, lands in DH-S-12 with the first folds registered after
// M1: the working directory (step 12.1) and the sandbox mode (step 12.3).
//
//dsh:test DH-T-VIEW-4 later DH-S-12

package views
