package model

import (
	"net/http"

	"github.com/smartpcr/agent-plugin/src/harness/internal/llm/transport"
)

// The test kit builds fixture clients and uses the seams: not reported.
var (
	_ = &http.Client{}
	_ = transport.Over(nil, transport.Options{})
	_ = http.DefaultTransport
)
