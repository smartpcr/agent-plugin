// Command echo is the launcher's test program: it serves JSON-RPC on its standard
// streams until they end. "home" answers DSH_HOME, "echo" its params and "spawn"
// starts a copy of itself that sleeps in the home (a grandchild the launcher's
// cleanup must reap) and answers its pid. "echo tree" starts a grandchild holding
// its output streams, prints the grandchild's pid and hangs.
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"time"

	"github.com/smartpcr/agent-plugin/src/sdk/jsonrpc"
)

func main() {
	if len(os.Args) > 1 && os.Args[1] == "sleep" {
		time.Sleep(time.Hour)
		return
	}
	if len(os.Args) > 1 && os.Args[1] == "tree" {
		hang()
		return
	}
	conn := jsonrpc.Serve(context.Background(), os.Stdin, os.Stdout, jsonrpc.Options{Methods: map[string]jsonrpc.Handler{
		"home": func(context.Context, json.RawMessage) (any, error) { return os.Getenv("DSH_HOME"), nil },
		"echo": func(_ context.Context, p json.RawMessage) (any, error) { return p, nil },
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

// hang starts a sleeping grandchild that inherits this process's output streams,
// prints its pid, and never exits: a run whose tree outlives its bound.
func hang() {
	exe, err := os.Executable()
	if err != nil {
		os.Exit(2)
	}
	p, err := os.StartProcess(exe, []string{exe, "sleep"}, &os.ProcAttr{Env: os.Environ(), Files: []*os.File{nil, os.Stdout, os.Stderr}})
	if err != nil {
		os.Exit(2)
	}
	fmt.Println(p.Pid)
	time.Sleep(time.Hour)
}
