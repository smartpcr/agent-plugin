package indirect

import "testing"

func FuzzDecode(f *testing.F) {
	f.Add(readSeed()) // want `f.Add argument readSeed\(\) is not compiled in \(calls readSeed\)`
	f.Fuzz(func(t *testing.T, b []byte) { Decode(b) })
}
