// Package x is seeded for the staticcheck gate: x == x is always true (SA4000).
package x

// Same compares x with itself.
func Same(x int) bool {
	if x == x {
		return true
	}
	return false
}
