// Package ops is a fixture package: allowed: the one importer of policy.
package ops

import (
	_ "github.com/smartpcr/agent-plugin/src/harness/internal/world/fs"
	_ "github.com/smartpcr/agent-plugin/src/harness/internal/world/sandbox/policy"
)
