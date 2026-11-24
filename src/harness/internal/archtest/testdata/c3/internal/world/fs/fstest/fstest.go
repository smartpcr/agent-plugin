// Package fstest is a fixture package: allowed: a fake imports a fake.
package fstest

import (
	_ "github.com/smartpcr/agent-plugin/src/harness/internal/world/fs"
	_ "github.com/smartpcr/agent-plugin/src/harness/internal/world/sandbox/sandboxtest"
)
