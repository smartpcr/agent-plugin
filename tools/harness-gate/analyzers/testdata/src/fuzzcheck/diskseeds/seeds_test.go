package diskseeds

import (
	"os" // want "seeds_test.go imports os"
	"testing"
)

func FuzzDecode(f *testing.F) { // want "FuzzDecode registers no seeds from seeds_test.go"
	b, _ := os.ReadFile("seed")
	f.Fuzz(func(t *testing.T, in []byte) { Decode(append(b, in...)) })
}
