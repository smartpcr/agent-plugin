// Package launch is the sandbox launch stage (C36, DH-D-SANDBOX-2): the hidden
// mode `dsh sandbox-launch`, which the host starts as the child of every command
// it runs for an agent, under the backend the seam selected. The launcher is the
// dsh binary itself; no helper is installed.
//
// The stage and the host talk over a pair of pipes the stage inherits and marks
// close-on-exec (DH-ADR-SANDBOX-2, INV-14). For a confined plan the stage
// confines itself and tries to open the canary for writing; for an unconfined
// plan (danger-full-access, backend none) it does neither and reports canary
// not-tried. It then sends ready{backend, governed effects, canary} and waits.
// Only after the host's go does it become the command; a rejection, or end of
// file in either direction, ends the stage with no command run.
//
// The frame codec, the stage's argument list (Plan) and the host's end of the
// pair (Channel) live here so the stage and the host share one definition; the
// host's decision is the seam's (world/sandbox Confirm, DH-D-SANDBOX-3).
//
//dsh:untrusted-parser the ready, go and exec-failed frames read from the other end of the pair
package launch
