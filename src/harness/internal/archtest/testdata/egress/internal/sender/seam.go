package sender

import (
	"github.com/smartpcr/agent-plugin/src/harness/internal/llm/transport"
	e "github.com/smartpcr/agent-plugin/src/harness/internal/util/egress"
)

var (
	_ = transport.Over(nil, transport.Options{}) // seeded: transport.Over outside tests
	_ = e.Over(nil)                              // seeded: renamed egress.Over outside llm/transport
	_ = transport.Options{}
)
