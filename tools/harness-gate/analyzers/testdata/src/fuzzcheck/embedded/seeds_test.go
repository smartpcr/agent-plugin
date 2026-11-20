package embedded

import (
	_ "embed"
	"testing"
)

//go:embed testdata/seed.bin
var seed []byte

func addDecodeSeeds(f *testing.F) { f.Add(seed) }
