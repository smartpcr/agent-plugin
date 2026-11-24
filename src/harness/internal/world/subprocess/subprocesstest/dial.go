package subprocesstest

import (
	"fmt"
	"io"
	"net"
	"os"
)

// dial connects, says so on the connection and on out, and parks with the
// connection open.
func dial(args []string, out io.Writer) int {
	if len(args) != 2 {
		return fail(fmt.Errorf("dial wants a network and an address, got %d arguments", len(args)))
	}
	c, err := net.Dial(args[0], args[1])
	if err != nil {
		return fail(err)
	}
	line := fmt.Sprintf("dialed %d\n", os.Getpid())
	if _, err := io.WriteString(c, line); err != nil {
		return fail(err)
	}
	fmt.Fprint(out, line)
	park()
	_ = c.Close()
	return 0
}
