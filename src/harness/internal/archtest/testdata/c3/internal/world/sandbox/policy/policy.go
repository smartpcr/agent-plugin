// Package policy is a fixture package: seeded: policy imports world/fs/local.
package policy

import (
	_ "github.com/smartpcr/agent-plugin/src/harness/internal/world/fs"
	_ "github.com/smartpcr/agent-plugin/src/harness/internal/world/fs/local"
	_ "github.com/smartpcr/agent-plugin/src/harness/internal/world/sandbox"
)
