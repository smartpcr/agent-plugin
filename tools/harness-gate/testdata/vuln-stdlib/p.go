// Package p calls strings.ToUpper, which the fake database in testdata/vulndb lists
// as vulnerable in every standard library before go1.99.0.
package p

import "strings"

// Up returns s in upper case.
func Up(s string) string { return strings.ToUpper(s) }
