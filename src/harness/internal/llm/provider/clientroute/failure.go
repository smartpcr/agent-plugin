package clientroute

import (
	"fmt"

	"github.com/smartpcr/agent-plugin/src/harness/internal/llm/vocab"
)

// Fail is a failure of code, classed from the code. A message never carries the
// client's own output (R9 of both route records): callers pass fixed text and
// values the harness chose.
func Fail(code vocab.Code, format string, args ...any) *vocab.Failure {
	f, err := vocab.NewFailure(code, fmt.Sprintf(format, args...))
	if err != nil {
		panic(err) // every caller names a code of the closed table
	}
	return f
}
