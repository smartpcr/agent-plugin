// Package missing is marked but has no fuzz target (and no test file at all).
package missing

//dsh:untrusted-parser // want "package is marked //dsh:untrusted-parser but has no fuzz target"

// Decode parses untrusted bytes.
func Decode(b []byte) int { return len(b) }
