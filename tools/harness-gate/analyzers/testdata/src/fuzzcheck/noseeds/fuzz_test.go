package noseeds

import "testing"

func FuzzDecode(f *testing.F) { // want "FuzzDecode: package is marked //dsh:untrusted-parser but has no seeds_test.go"
	f.Add([]byte("x"))
	f.Fuzz(func(t *testing.T, b []byte) { Decode(b) })
}
