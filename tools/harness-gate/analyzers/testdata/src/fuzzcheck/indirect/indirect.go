// Package indirect registers a seed that a helper of another test file reads from disk.
package indirect

//dsh:untrusted-parser

// Decode parses untrusted bytes.
func Decode(b []byte) int { return len(b) }
