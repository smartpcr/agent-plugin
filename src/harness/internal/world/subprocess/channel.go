package subprocess

import (
	"os"

	"github.com/smartpcr/agent-plugin/src/harness/internal/util/herr"
)

// Channel is a private byte channel a child inherits beside its standard streams,
// such as the status channel of a launch stage (execution.md sec. 6.1). Nothing
// else of the host is inherited: a channel is the only way to hand the child
// another open file.
type Channel struct {
	// File is the child's end, typically one end of an os.Pipe. Start hands it to
	// the child and leaves the host's copy open; the caller closes it once Start
	// has returned, so that end of file reaches the other end when the child is
	// done with it.
	File *os.File
}

// ChildFD is the number the child sees channel i of extra under: descriptor 3+i
// on POSIX (os/exec's ExtraFiles), the inherited handle's own value on Windows.
// The caller tells the child the number, in an argument or an environment entry,
// and the child opens it with os.NewFile.
func ChildFD(extra []Channel, i int) uintptr { return childFD(extra, i) }

// checkExtra refuses a channel without a file.
func checkExtra(extra []Channel) error {
	for i, c := range extra {
		if c.File == nil {
			return herr.Newf(ReasonBadArgument, "channel %d has no file", i)
		}
	}
	return nil
}
