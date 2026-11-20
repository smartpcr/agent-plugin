package indirect

import "os"

func readSeed() []byte {
	b, _ := os.ReadFile("testdata/seed.bin")
	return b
}
