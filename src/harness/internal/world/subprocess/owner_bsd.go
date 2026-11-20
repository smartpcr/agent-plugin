//go:build unix && !linux

package subprocess

// No per-tree container exists here; the process group is the documented owner
// (DH-D-EXEC-2), not a fallback.
func choose() Owner { return Owner{Kind: ProcessGroup} }

func newOwner() (treeOwner, error) { return &groupOwner{}, nil }
