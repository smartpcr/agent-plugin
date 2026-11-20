package yamlx

import (
	"testing"
)

// parseSeeds are inputs for FuzzParse, compiled into the test binary.
var parseSeeds = []string{
	"",
	"# only a comment\n",
	"a: 1\n",
	"- id: a # trailing\n  config:\n    port: !expr env.PORT ?? 3080\n",
	"a: &x 1\nb: *x\n",
	"a: 1\n---\nb: 2\n",
	"m:\n  <<: {a: 1}\n",
	"a: 1\na: 2\n",
	"a: !foo 1\n",
	"a: [1\n",
	"a: 017\nb: 0b101\nc: 0x1F\nd: 0o17\ne: 1.5e3\nf: .inf\ng: yes\n",
	"text: |\n  hello\n  world\n",
	"? [a, b]\n: c\n",
	"[[[[[[[[[[1]]]]]]]]]]\n",
	"\"\\ud800\": 1\n",
	"\t- x\n",
}

// FuzzParse feeds untrusted YAML to Parse and Edit: neither may panic, and a
// document that parses edits to text that parses back to the same data.
func FuzzParse(f *testing.F) {
	for _, s := range parseSeeds {
		f.Add([]byte(s))
	}
	f.Fuzz(func(t *testing.T, data []byte) {
		doc, err := Parse("fuzz.yaml", data, WithTag("!expr"), WithLimits(1<<16, 32))
		if err != nil {
			if _, ok := err.(*SyntaxError); !ok {
				t.Fatalf("Parse error is %T, want *SyntaxError: %v", err, err)
			}
			return
		}
		out, err := doc.Edit()
		if err != nil {
			return
		}
		back, err := Parse("fuzz.yaml", out, WithTag("!expr"), WithLimits(1<<20, 32))
		if err != nil {
			t.Fatalf("edited text does not parse: %v\n%s", err, out)
		}
		if !Equal(back.Value(), doc.Value()) {
			t.Fatalf("edited text holds other data:\n%s", out)
		}
	})
}
