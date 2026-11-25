//go:build !linux

package subprocess

import "context"

// prepareOwner only fixes the choice: no user service manager owns trees here.
func prepareOwner(context.Context, ProviderOptions) { _ = TreeOwner() }

func delegatedScope() string { return "" }

// ownerCgroup is always "": control groups are Linux's.
func ownerCgroup(treeOwner) string { return "" }
