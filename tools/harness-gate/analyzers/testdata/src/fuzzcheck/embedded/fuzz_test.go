package embedded

import "testing"

func FuzzDecode(f *testing.F) {
	addDecodeSeeds(f)
	f.Fuzz(func(t *testing.T, b []byte) { Decode(b) })
}
