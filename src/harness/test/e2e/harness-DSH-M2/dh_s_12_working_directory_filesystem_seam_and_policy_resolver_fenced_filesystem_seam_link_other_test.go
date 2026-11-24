//go:build e2e && !windows

package e2e

import "os"

// ffsMakeLink makes link a symbolic link to target and returns the kind it made.
func ffsMakeLink(target, link string) (string, error) {
	return "symlink", os.Symlink(target, link)
}
