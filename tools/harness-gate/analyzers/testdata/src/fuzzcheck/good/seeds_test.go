package good

import "testing"

var decodeSeeds = [][]byte{[]byte("{}"), []byte("["), nil}

func FuzzDecode(f *testing.F) {
	for _, s := range decodeSeeds {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, b []byte) { Decode(b) })
}
