package launcher

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"

	"github.com/smartpcr/agent-plugin/src/harness/internal/world/subprocess"
)

var (
	treesMu sync.Mutex
	// trees are the running trees Spawn and Start started, by the top-level test
	// that started them, so a scenario past its deadline can stop them (StopTrees).
	trees = map[string][]*subprocess.Process{}
)

// topTest is the name of the top-level test of the test named name.
func topTest(name string) string {
	top, _, _ := strings.Cut(name, "/")
	return top
}

// register records proc under t's top-level test until t ends.
func register(t testing.TB, proc *subprocess.Process) {
	name := topTest(t.Name())
	treesMu.Lock()
	trees[name] = append(trees[name], proc)
	treesMu.Unlock()
	t.Cleanup(func() {
		treesMu.Lock()
		defer treesMu.Unlock()
		kept := trees[name][:0]
		for _, p := range trees[name] {
			if p != proc {
				kept = append(kept, p)
			}
		}
		if len(kept) == 0 {
			delete(trees, name)
		} else {
			trees[name] = kept
		}
	})
}

// StopTrees force-stops every tree that the top-level test named test (or one of
// its subtests) started through Spawn or Start and that is still recorded, each
// within 3*Grace, and returns the stops' errors. A blocked test whose children
// are stopped sees them exit, so its waits end.
func StopTrees(test string) error {
	treesMu.Lock()
	procs := append([]*subprocess.Process(nil), trees[topTest(test)]...)
	treesMu.Unlock()
	var errs []error
	for _, p := range procs {
		ctx, cancel := context.WithTimeout(context.Background(), 3*Grace)
		errs = append(errs, p.StopTree(ctx, 0))
		cancel()
	}
	return errors.Join(errs...)
}
