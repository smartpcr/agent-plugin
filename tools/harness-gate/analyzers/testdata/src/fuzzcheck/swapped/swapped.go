// Package swapped passes its fuzz target's F to the unseeded parameter of a helper.
package swapped

//dsh:untrusted-parser

// Decode parses untrusted bytes.
func Decode(b []byte) int { return len(b) }
