package tools

import (
	"context"

	htools "github.com/smartpcr/agent-plugin/src/harness/internal/tools"
	"github.com/smartpcr/agent-plugin/src/harness/internal/world/shellexec"
)

// StreamOf and Budget expose stream and budget to the external tests.
var (
	StreamOf = stream
	Budget   = budget
)

// RunBash runs the bash tool's call body on deps outside the pipeline, so a
// test can hand it a call the pipeline never would (no session).
func RunBash(ctx context.Context, deps Deps, in In, run htools.Run) (Result, error) {
	return (&shell{name: BashName, dialect: shellexec.Bash, deps: deps}).run(ctx, in, run)
}
