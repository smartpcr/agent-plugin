// Package noseeds has a fuzz target but no seeds_test.go.
package noseeds

//dsh:untrusted-parser

// Decode parses untrusted bytes.
func Decode(b []byte) int { return len(b) }
