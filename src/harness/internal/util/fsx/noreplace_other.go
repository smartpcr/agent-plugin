//go:build unix && !linux

package fsx

func renameNoReplace(stage, target string, _ bool) error {
	return linkThenRemove(stage, target)
}
