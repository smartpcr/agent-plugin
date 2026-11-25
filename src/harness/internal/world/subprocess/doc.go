// Deferred tests (decision M1-D-CLOSE-5; impl.md's DH-S-13 section names each
// one). The package doc is in subprocess.go. None is deferred now: the stop
// ladder's first causes (DH-T-EXEC-6, H-25) and the trusted mark, PeerTree and
// Limits (DH-T-EXEC-17, H-26) were built by gap-closure steps 19.4.3 to 19.4.5,
// and H-24's run and capture (DH-T-EXECA-1 to -3) and creation inside the
// Windows job in one call (DH-T-NODEPROC-8) by steps 19.3.1 and 19.3.5.

package subprocess
