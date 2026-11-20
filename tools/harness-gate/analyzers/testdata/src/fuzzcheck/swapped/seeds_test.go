package swapped

import "testing"

func addSeeds(unused, seeded *testing.F) { seeded.Add([]byte("x")) }

func FuzzDecode(f *testing.F) { // want `FuzzDecode registers no seeds from seeds_test.go`
	addSeeds(f, new(testing.F))
	f.Fuzz(func(t *testing.T, b []byte) { Decode(b) })
}

func FuzzDecodeBound(f *testing.F) {
	addSeeds(new(testing.F), f)
	f.Fuzz(func(t *testing.T, b []byte) { Decode(b) })
}
