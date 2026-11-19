// Package cleanup is the fixture of the cleanup analyzer.
package cleanup

// Spawn may start a goroutine: it is not in a test file.
func Spawn() { go func() {}() }
