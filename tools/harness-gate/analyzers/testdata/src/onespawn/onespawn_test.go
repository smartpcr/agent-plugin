package onespawn

import (
	"os/exec"
	"testing"
)

func TestToolchain(t *testing.T) {
	_ = exec.Command("go", "version")
}
