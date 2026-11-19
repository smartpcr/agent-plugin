package p

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
)

func TestParent(t *testing.T) {
	if got := Parent(1); got != 3 {
		t.Fatalf("Parent(1) = %d", got)
	}
}

// TestReexecChild re-executes the test binary; the child runs Reexec. The child is
// started through m.Run with -test.gocoverdir=$GOCOVERDIR so its counters land
// where the gate collects them.
func TestReexecChild(t *testing.T) {
	if os.Getenv("COVCHILD_ROLE") == "child" {
		if got := Reexec(1); got != 4 {
			t.Fatalf("Reexec(1) = %d", got)
		}
		return
	}
	if os.Getenv("COVCHILD_NO_REEXEC") != "" {
		return
	}
	cmd := exec.Command(os.Args[0], "-test.run=^TestReexecChild$", "-test.gocoverdir="+os.Getenv("GOCOVERDIR"))
	cmd.Env = append(os.Environ(), "COVCHILD_ROLE=child")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("child: %v\n%s", err, out)
	}
}

// TestHelperBinary builds cmd/helper with -cover and runs it; the helper inherits
// GOCOVERDIR and writes its counters there on exit.
func TestHelperBinary(t *testing.T) {
	if os.Getenv("COVCHILD_NO_HELPER") != "" {
		return
	}
	bin := filepath.Join(t.TempDir(), "helper")
	if runtime.GOOS == "windows" {
		bin += ".exe"
	}
	build := exec.Command("go", "build", "-cover", "-covermode=set", "-o", bin, "example.com/covchild/cmd/helper")
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build helper: %v\n%s", err, out)
	}
	if out, err := exec.Command(bin).CombinedOutput(); err != nil || string(out) != "9\n" {
		t.Fatalf("helper: %v %q", err, out)
	}
}
