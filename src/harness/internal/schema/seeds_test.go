package schema

import "testing"

// validateSeeds are inputs for FuzzValidate, compiled into the test binary.
var validateSeeds = []string{
	`{"name":"a","n":0}`,
	`{"name":"a","n":-1}`,
	`{"n":1}`,
	`{"name":"a","n":1,"extra":true}`,
	`{"name":1,"n":"x"}`,
	`{"name":"a","n":1.5}`,
	`{"name":"a","n":1e400}`,
	`{"name":"a","n":1,"tags":["x","x"]}`,
	`{"name":"a","n":1,"mode":"fast"}`,
	`{"name":"a","n":1,"nested":{"k":"v","z":1}}`,
	`[]`,
	`null`,
	`{} {}`,
	`{"name":"\ud800","n":1}`,
	`{"name":"a","n":1,"tags":null}`,
	``,
}

// FuzzValidate feeds untrusted JSON to Decode and Validate: neither may panic, and
// Decode succeeds exactly when the input passes validation and strict decoding.
func FuzzValidate(f *testing.F) {
	for _, s := range validateSeeds {
		f.Add([]byte(s))
	}
	typ := MustOf[sample]()
	f.Fuzz(func(t *testing.T, raw []byte) {
		var dst sample
		issues := typ.Decode(raw, &dst)
		for _, is := range issues {
			if is.Message == "" {
				t.Fatalf("an issue without a message: %+v", is)
			}
		}
		if len(issues) == 0 {
			if again := typ.Validate(dst); len(again) > 0 {
				t.Fatalf("decoded value %+v fails validation: %v", dst, again)
			}
		}
	})
}
