package caltime

import "testing"

// parseSeeds are the compiled-in seeds of FuzzParse (QG-6): stored instants with
// and without fractions and offsets, a leap second, a zone name, the unknown-offset
// marker and a bare date.
var parseSeeds = []string{
	"2099-01-01T01:00:00.000Z",
	"2099-01-01T09:00:00.5+08:00",
	"2016-12-31T23:59:60Z",
	"Asia/Tokyo",
	"-00:00",
	"2021-03-14",
}

// addParseSeeds registers parseSeeds with f.
func addParseSeeds(f *testing.F) {
	for _, s := range parseSeeds {
		f.Add(s)
	}
}
