//go:build !windows

package local_test

import "os"

// makeLink makes link a symbolic link to target and returns the kind it made.
func makeLink(target, link string) (string, error) {
	return "symlink", os.Symlink(target, link)
}
