// Command echo is the launcher's test program: it serves JSON-RPC on its standard
// streams until they end. "home" answers DSH_HOME, "echo" its params, "env"
// whether each name of its params is set, and "spawn" starts a copy of itself
// that sleeps in the home (a grandchild the launcher's cleanup must reap) and
// answers its pid. "echo tree" starts a grandchild holding
// its output streams, prints the grandchild's pid and hangs; "echo hold" does the
// same but exits, leaving the grandchild holding the streams. "echo flood" writes
// 64 KiB to its standard output. "echo tags" prints
// the build tags of tags_*.go it was built with, sorted, comma-separated.
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"slices"
	"strings"
	"time"

	"github.com/smartpcr/agent-plugin/src/sdk/jsonrpc"
)

// tags is appended to by the init of each tags_*.go the build tags select.
var tags []string

func main() {
	if len(os.Args) > 1 && os.Args[1] == "tags" {
		slices.Sort(tags)
		fmt.Println(strings.Join(tags, ","))
		return
	}
	if len(os.Args) > 1 && os.Args[1] == "sleep" {
		time.Sleep(time.Hour)
		return
	}
	if len(os.Args) > 1 && (os.Args[1] == "tree" || os.Args[1] == "hold") {
		hang(os.Args[1] == "tree")
		return
	}
	if len(os.Args) > 1 && os.Args[1] == "flood" {
		_, _ = os.Stdout.WriteString(strings.Repeat("x", 64<<10))
		return
	}
	conn := jsonrpc.Serve(context.Background(), os.Stdin, os.Stdout, jsonrpc.Options{Methods: map[string]jsonrpc.Handler{
		"home": func(context.Context, json.RawMessage) (any, error) { return os.Getenv("DSH_HOME"), nil },
		"echo": func(_ context.Context, p json.RawMessage) (any, error) { return p, nil },
		"env": func(_ context.Context, p json.RawMessage) (any, error) {
			var names []string
			if err := json.Unmarshal(p, &names); err != nil {
				return nil, err
			}
			set := map[string]bool{}
			for _, n := range names {
				_, set[n] = os.LookupEnv(n)
			}
			return set, nil
		},
		"spawn": func(context.Context, json.RawMessage) (any, error) {
			exe, err := os.Executable()
			if err != nil {
				return nil, err
			}
			null, err := os.OpenFile(os.DevNull, os.O_RDWR, 0)
			if err != nil {
				return nil, err
			}
			defer null.Close()
			p, err := os.StartProcess(exe, []string{exe, "sleep"}, &os.ProcAttr{Dir: os.Getenv("DSH_HOME"), Env: os.Environ(), Files: []*os.File{null, null, null}})
			if err != nil {
				return nil, err
			}
			return p.Pid, nil
		},
	}})
	<-conn.Done()
}

// hang starts a sleeping grandchild that inherits this process's output streams
// and prints its pid; with forever it never exits: either way a run whose tree
// outlives its bound.
func hang(forever bool) {
	exe, err := os.Executable()
	if err != nil {
		os.Exit(2)
	}
	p, err := os.StartProcess(exe, []string{exe, "sleep"}, &os.ProcAttr{Env: os.Environ(), Files: []*os.File{nil, os.Stdout, os.Stderr}})
	if err != nil {
		os.Exit(2)
	}
	fmt.Println(p.Pid)
	if forever {
		time.Sleep(time.Hour)
	}
}
