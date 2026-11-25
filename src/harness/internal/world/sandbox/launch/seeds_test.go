package launch

import "testing"

// readyBodies are frame bodies that FuzzDecodeReady's seeder frames with their own
// length (QG-6): valid readies of each canary result, then an unknown field, a
// second JSON value and the other two messages.
var readyBodies = []string{
	`{"msg":"ready","backend":"none","governed":[],"canary":"not-tried"}`,
	`{"msg":"ready","backend":"bwrap","governed":["fs-write","network"],"canary":"refused"}`,
	`{"msg":"ready","backend":"landlock","governed":["fs-write"],"canary":"written"}`,
	`{"msg":"ready","backend":"none","governed":[],"canary":"not-tried","extra":1}`,
	`{"msg":"ready","backend":"none","governed":[],"canary":"not-tried"}{}`,
	`{"msg":"go"}`,
	`{"msg":"exec-failed","error":"no such file"}`,
}

// rawSeeds are seeds whose header lies: a cut header, a length longer than the
// body, a zero and an oversized length, and a valid frame with a trailing byte.
var rawSeeds = [][]byte{
	{0, 0},
	append([]byte{0, 0, 0, 40}, `{"msg":"ready"`...),
	{0, 0, 0, 0},
	{0, 0, 0x10, 0x01},
	append(append([]byte{0, 0, 0, 2}, "{}"...), 'x'),
}

// addReadySeeds registers the seeds with f.
func addReadySeeds(f *testing.F) {
	for _, s := range readyBodies {
		n := len(s)
		f.Add(append([]byte{byte(n >> 24), byte(n >> 16), byte(n >> 8), byte(n)}, s...))
	}
	for _, b := range rawSeeds {
		f.Add(b)
	}
}
