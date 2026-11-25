// Deferred tests (decision M1-D-CLOSE-5; impl.md's DH-S-16 section names each
// one). The package doc is in profile.go. Step 6.7 is built with H-28 held open
// in the DH-S-6 evidence record: profile reset (DH-T-BOOT-8) and the safe-mode
// launcher layer (DH-T-BOOT-23) are built by gap-closure plan step 19.5.5 and
// proven in DH-S-16, whose admin tree and launchers offer them.
//
//dsh:test DH-T-BOOT-8 later DH-S-16
//dsh:test DH-T-BOOT-23 later DH-S-16

package profile
