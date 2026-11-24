//go:build unix && !linux

package fsx

func renameNoReplace(stage, target string, _ bool, _ func() error) error {
	return linkThenRemove(stage, target)
}
