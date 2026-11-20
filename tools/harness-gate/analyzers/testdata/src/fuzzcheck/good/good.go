// Package good follows the convention with a Go slice of seeds.
//
//dsh:untrusted-parser frames read from a peer
package good

// Decode parses untrusted bytes.
func Decode(b []byte) int { return len(b) }
