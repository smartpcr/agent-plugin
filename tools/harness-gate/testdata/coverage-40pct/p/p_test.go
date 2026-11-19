package p

import "testing"

func TestGradeA(t *testing.T) {
	if got := Grade(95); got != "A" {
		t.Fatalf("Grade(95) = %q, want A", got)
	}
}
