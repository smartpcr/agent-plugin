//go:build replay

// Package replay is the keyless replay runner (DH-D-UTIL-7, C54): each
// directory under testdata is a committed recording, replayed by the built dsh
// into a scratch home with no key, and its persisted log (read back from the
// run's SQLite store), its workspace tree and its model requests compared, after
// the fixed typed-token normalisation of testkit/fixture, with the committed
// expected files.
//
// A scenario directory holds:
//
//	scenario.json        the manifest (fixture.Manifest)
//	session.jsonl        the recording: the normalised export the replay plays
//	workspace/           the workspace the run starts in
//	workspace.expected/  the workspace the run must leave
//	log.expected.jsonl   the normalised export the run must persist
//	requests.expected/   the normalised model requests, NNN.json in attempt order
//
// DSH_REPLAY_REFRESH=1 replays without the request check and rewrites
// session.jsonl, log.expected.jsonl and requests.expected from the run, for an
// authored script's first run or a reviewed prompt change; it never rewrites
// workspace.expected, which is the human-reviewed half of the oracle.
//
// H-10 (sessions with children) is recorded open: no scenario here spawns a
// child session, and the runner exports the one root session.
package replay
