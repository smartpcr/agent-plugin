package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/smartpcr/agent-plugin/src/sdk/protocol"
)

func TestRun(t *testing.T) {
	var stderr bytes.Buffer
	if code := run(nil, &stderr); code != 2 || !strings.Contains(stderr.String(), "usage") {
		t.Fatalf("run() = %d, %q", code, stderr.String())
	}
	out := filepath.Join(t.TempDir(), "schema.json")
	if code := run([]string{out}, &stderr); code != 0 {
		t.Fatalf("run = %d, %q", code, stderr.String())
	}
	got, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	if want, _ := protocol.Document(); !bytes.Equal(got, want) {
		t.Fatal("genschema wrote something other than protocol.Document()")
	}
	stderr.Reset()
	if code := run([]string{filepath.Join(t.TempDir(), "missing", "schema.json")}, &stderr); code != 1 || !strings.Contains(stderr.String(), "genschema:") {
		t.Fatalf("run into a missing directory = %d, %q", code, stderr.String())
	}
}
