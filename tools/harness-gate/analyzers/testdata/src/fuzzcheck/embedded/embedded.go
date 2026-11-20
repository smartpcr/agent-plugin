// Package embedded registers //go:embed seeds through a seeds_test.go helper.
package embedded

//dsh:untrusted-parser

// Decode parses untrusted bytes.
func Decode(b []byte) int { return len(b) }
