package good

import "testing"

var decodeSeeds = [][]byte{[]byte("{}"), []byte("["), nil}

func FuzzDecode(f *testing.F) {
	// The seed loop and the fuzz callback each declare their own b.
	for _, b := range decodeSeeds {
		f.Add(b)
	}
	f.Fuzz(func(t *testing.T, b []byte) { Decode(b) })
}
