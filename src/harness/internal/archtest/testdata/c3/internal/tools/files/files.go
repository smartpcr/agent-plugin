// Package files is a fixture package: seeded: a tool imports policy and a fake.
package files

import (
	_ "github.com/smartpcr/agent-plugin/src/harness/internal/world/fs/fstest"
	_ "github.com/smartpcr/agent-plugin/src/harness/internal/world/fs/ops"
	_ "github.com/smartpcr/agent-plugin/src/harness/internal/world/sandbox/policy"
)
