//go:build windows

package sdk

import "os"

// terminate has no polite signal to send on Windows: the end of the host's
// input, the shutdown rung, is the polite stop there.
func terminate(*os.Process) error { return errNothingToSend }
