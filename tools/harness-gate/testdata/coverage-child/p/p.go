// Package p is covered in three processes: the test process runs Parent, a
// re-executed test binary runs Reexec and a separately built helper binary runs
// Helper. Only the merge of all three reaches the floor.
package p

// Parent is run by the test process.
func Parent(n int) int {
	a := n + 1
	b := a * 2
	c := b - n
	return c
}

// Reexec is run only by the test binary re-executed as a child.
func Reexec(n int) int {
	a := n * 3
	b := a + 1
	return b
}

// Helper is run only by the helper binary.
func Helper(n int) int {
	a := n * 5
	b := a - 1
	return b
}
