package kv

import "github.com/smartpcr/agent-plugin/src/go/cordis"

// DBKey is the one database handle, for a component that keeps a machine-state
// namespace of its own (host.auth's session table).
var DBKey = cordis.NewServiceKey[DB]("storage.db")
