package main

import (
	"errors"
	"flag"
	"os"
	"os/exec"
	"strings"
	"testing"
	"testing/synctest"

	"github.com/smartpcr/agent-plugin/src/go/cordis"
)

// childEnv marks the re-executed test binary that runs main itself.
const childEnv = "CORDIS_SAMPLE_RUN_MAIN"

// TestMainRunsWalkthrough runs the real main in a re-executed child of this test
// binary: real clock, real stdout, the JSON file in os.TempDir. The child writes its
// coverage to the parent's -test.gocoverdir, so the coverage gate merges it.
func TestMainRunsWalkthrough(t *testing.T) {
	if os.Getenv(childEnv) == "1" {
		main()
		return
	}
	tmp := t.TempDir()
	args := []string{"-test.run=^TestMainRunsWalkthrough$", "-test.count=1"}
	if dir := flag.Lookup("test.gocoverdir"); dir != nil && dir.Value.String() != "" {
		args = append(args, "-test.gocoverdir="+dir.Value.String())
	}
	cmd := exec.Command(os.Args[0], args...)
	cmd.Env = append(os.Environ(), childEnv+"=1", "TMPDIR="+tmp, "TMP="+tmp, "TEMP="+tmp)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("main failed: %v\n%s", err, out)
	}
	for _, want := range []string{
		"heartbeat before database:   Pending",
		"heartbeat after database:    Active",
		"heartbeat restored:          Active",
		"loaded from " + tmp,
	} {
		if !strings.Contains(string(out), want) {
			t.Errorf("main output lacks %q:\n%s", want, out)
		}
	}
	if _, err := os.Stat(tmp + string(os.PathSeparator) + "cordis-sample.json"); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("main left its config file behind: %v", err)
	}
}

func TestHeartbeatRejectsIntervalOutOfRange(t *testing.T) {
	for _, ms := range []int{9, 60_001} {
		err := HeartbeatOptions{IntervalMs: ms}.Validate()
		var configError *cordis.ConfigError
		if !errors.As(err, &configError) || configError.Issues[0].Path != "intervalMs" {
			t.Errorf("IntervalMs %d: got %v, want a ConfigError at intervalMs", ms, err)
		}
	}
	if err := (HeartbeatOptions{IntervalMs: 10}).Validate(); err != nil {
		t.Errorf("IntervalMs 10: %v", err)
	}
}

func TestSecondDatabaseFailsToProvide(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		root := cordis.New()
		root.Loggers().ClearExporters()
		env := sampleEnv{Settle: synctest.Wait}
		if err := use(env, root, DatabaseComponent, nil); err != nil {
			t.Fatalf("first database: %v", err)
		}
		err := use(env, root, DatabaseComponent, nil)
		if err == nil || !strings.Contains(err.Error(), `service "database" is already registered`) {
			t.Errorf("second database: got %v, want an already-registered error", err)
		}
		root.Dispose()
	})
}

func TestUseOnDisposedContextFails(t *testing.T) {
	root := cordis.New()
	root.Loggers().ClearExporters()
	child, err := root.Apply("child", func(*cordis.Context) error { return nil })
	if err != nil {
		t.Fatal(err)
	}
	child.Dispose()
	if err := use(sampleEnv{Settle: func() {}}, child.Ctx(), DatabaseComponent, nil); !errors.Is(err, cordis.ErrInactive) {
		t.Errorf("use on a disposed context: got %v, want ErrInactive", err)
	}
}
