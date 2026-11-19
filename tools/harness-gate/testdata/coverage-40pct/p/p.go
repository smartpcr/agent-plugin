// Package p seeds a package under the coverage floor: its test covers 2 of 5
// statements (40%).
package p

// Grade grades a score.
func Grade(n int) string {
	if n > 90 {
		return "A"
	}
	if n > 80 {
		return "B"
	}
	return "C"
}
