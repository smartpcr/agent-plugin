//go:build !windows

package local

// Platform is the backend chain of this platform, in selection order. The Linux
// chain (bubblewrap, then Landlock) is DH-D-SANDBOX-4 (impl.md 13.4); until it
// lands the chain is empty and a confined policy is refused with
// sandbox/unavailable, never run unconfined.
func Platform() []Backend { return nil }
