package version

import "testing"

// parseSeeds are the compiled-in seeds of FuzzParse (QG-6): exact versions with and
// without pre-release and build parts, a short "v" version, and ranges with an
// alternative, a hyphen and a tilde.
var parseSeeds = []string{"1.2.3", "1.2.3-rc.1+b", "v1", "^1.2 || 2.x", "1.2 - 3", "~0.0.1-0"}

// addParseSeeds registers parseSeeds with f.
func addParseSeeds(f *testing.F) {
	for _, s := range parseSeeds {
		f.Add(s)
	}
}
