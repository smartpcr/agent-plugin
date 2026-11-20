// Package diskseeds reads its seeds from disk at run time.
package diskseeds

//dsh:untrusted-parser

// Decode parses untrusted bytes.
func Decode(b []byte) int { return len(b) }
