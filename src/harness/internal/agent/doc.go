// Deferred tests (decision M1-D-CLOSE-5; impl.md's DH-S-15 section names it).
// The package doc is in agent.go. Step 5.11 is built with H-39 held open in the
// DH-S-5 evidence record (gap M1-G-CLOSE-9.1): DH-T-AGENT-14, the frames of the
// assistant stream notice (one Start, dense indexes, one End after the record
// is committed, abandoned when the append fails). No M1 code emits the frames;
// their first consumer is the SDK server's stream notices of step 15.7.
//
//dsh:test DH-T-AGENT-14 later DH-S-15

package agent
