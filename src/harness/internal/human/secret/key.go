package secret

import "github.com/smartpcr/agent-plugin/src/go/cordis"

// Key is the service key of the process's one secret store. The component that
// owns the store provides it; consumers such as human.secret.redact inject it.
var Key = cordis.NewServiceKey[*Store]("secret.store")
