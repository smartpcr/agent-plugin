package policy

import (
	"os"
	"slices"
	"testing"
)

// ShippedPlatforms is the platforms whose policy text ships, sorted.
func ShippedPlatforms() []string {
	var out []string
	for p := range texts {
		out = append(out, p)
	}
	slices.Sort(out)
	return out
}

// LoadTextDir is loadTexts over the directory dir.
func LoadTextDir(dir string) (map[string]string, error) { return loadTexts(os.DirFS(dir)) }

// WithoutShippedTexts ships no text for the rest of t.
func WithoutShippedTexts(t testing.TB) {
	saved := texts
	texts = map[string]string{}
	t.Cleanup(func() { texts = saved })
}
