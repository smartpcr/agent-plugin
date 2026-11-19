// Package nosleep is the fixture of the nosleep analyzer.
package nosleep

import "time"

// Wait may sleep: it is not in a test file.
func Wait() { time.Sleep(time.Millisecond) }
