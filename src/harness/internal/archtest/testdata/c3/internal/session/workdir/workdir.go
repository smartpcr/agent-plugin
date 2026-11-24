// Package workdir is a fixture package: seeded: workdir imports world/fs/local.
package workdir

import (
	_ "github.com/smartpcr/agent-plugin/src/harness/internal/world/fs"
	_ "github.com/smartpcr/agent-plugin/src/harness/internal/world/fs/local"
)
