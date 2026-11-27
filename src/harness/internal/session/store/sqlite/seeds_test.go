package sqlite

import "testing"

// exportSeeds are the compiled-in seeds of FuzzParseExport (QG-6): an empty file,
// a line that is not JSON, an object without a kind, a header straight followed by
// a trailer, and a bare gzip magic.
var exportSeeds = []string{
	"",
	"not json\n",
	"{}\n",
	"{\"kind\":\"header\"}\n{\"kind\":\"trailer\"}\n",
	"\x1f\x8b",
}

// addExportSeeds registers exportSeeds with f.
func addExportSeeds(f *testing.F) {
	for _, s := range exportSeeds {
		f.Add([]byte(s))
	}
}
