package glob

import "testing"

// compileSeeds are the compiled-in pattern seeds of FuzzCompile (QG-6): a
// recursive wildcard, nested braces with a negated class, an escape, a class
// holding a bracket, a misplaced "**", an empty alternative and a trailing dash.
var compileSeeds = []string{"**/*.go", "{a,{b,c}}/[!x]?", `\[`, "[]a]", "a/**b", "{,}", "[a-]"}

// compileSeedName is the name every pattern seed is matched against.
const compileSeedName = "a/b/c.go"

// addCompileSeeds registers each of compileSeeds, with compileSeedName, with f.
func addCompileSeeds(f *testing.F) {
	for _, s := range compileSeeds {
		f.Add(s, compileSeedName)
	}
}
