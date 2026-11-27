package home

import "github.com/smartpcr/agent-plugin/src/go/cordis"

// Key is the service under which boot provides the launch's home layout on the
// root, before any entry loads. A component reads it with Lookup: boot, not an
// entry, provides it.
var Key = cordis.NewServiceKey[Layout]("boot.home")
