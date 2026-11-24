// Deferred tests (decision M1-D-CLOSE-5; impl.md's DH-S-13 section names each
// one). The package doc is in subprocess.go. Steps 6.2 and 6.3 are built with
// these parts held open in the DH-S-6 evidence record: the stop ladder's first
// causes (DH-T-EXEC-6) are H-25, and the trusted mark, PeerTree and Limits
// (DH-T-EXEC-17) are H-26. They are built by gap-closure plan stage 19.4 and
// proven in DH-S-13, where the first command for an agent starts. H-24's run and
// capture (DH-T-EXECA-1 to -3) and creation inside the Windows job in one call
// (DH-T-NODEPROC-8) were built by gap-closure steps 19.3.1 and 19.3.5.
//
//dsh:test DH-T-EXEC-6 later DH-S-13
//dsh:test DH-T-EXEC-17 later DH-S-13

package subprocess
