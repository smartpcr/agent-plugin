package nosleep

import (
	"testing"
	"time"
	tm "time"
)

type clock struct{}

func (clock) Sleep(time.Duration) {}

func TestDirect(t *testing.T) {
	time.Sleep(time.Millisecond) // want `time.Sleep in a test file`
}

func TestAliased(t *testing.T) {
	tm.Sleep(1) // want `time.Sleep in a test file`
}

func TestReference(t *testing.T) {
	pause := time.Sleep // want `time.Sleep in a test file`
	_ = pause
}

func TestShadowed(t *testing.T) {
	time := clock{}
	time.Sleep(1)
}

func TestVirtualTime(t *testing.T) {
	<-tm.After(time.Millisecond)
}
