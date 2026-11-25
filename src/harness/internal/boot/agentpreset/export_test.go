package agentpreset

import "github.com/smartpcr/agent-plugin/src/harness/internal/session/scope"

// TargetUsers is the number of bindings holding or waiting for k's target lock:
// lockTarget counts a binding before it blocks on the lock, so a test waits on
// this count instead of sleeping until a racing bind reaches the lock (QG-4).
func TargetUsers(r *Registry, k scope.Key) int {
	r.bindMu.Lock()
	defer r.bindMu.Unlock()
	if tl := r.targets[k]; tl != nil {
		return tl.users
	}
	return 0
}
