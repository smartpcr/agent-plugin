//go:build e2e && !linux

package e2e

import (
	"errors"
	"runtime"
)

// The machine agent of the Stage 8.5 confinement suite runs only on Linux; a
// host of another OS reaches a Linux machine through WSL or SSH.

func lcssLandlockABI() (int, error) {
	return 0, errors.New("Landlock is a Linux interface; this agent runs on " + runtime.GOOS)
}

func lcssDisableLandlock([]string) error {
	return errors.New("the unconfined machine is emulated only on Linux; this agent runs on " + runtime.GOOS)
}
