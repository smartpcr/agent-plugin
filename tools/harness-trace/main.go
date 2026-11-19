// Command harness-trace keeps the deepseek-harness to Go traceability ledger honest.
//
// It enumerates every upstream unit (packages, dependencies, decisions, tech debt and
// contract units), hands each unit to exactly one research agent, merges the rows those
// agents write, and verifies that nothing is left without a disposition.
//
//	go -C tools/harness-trace run . inventory   enumerate upstream units
//	go -C tools/harness-trace run . assign      split units between agents (clusters.json)
//	go -C tools/harness-trace run . handoff     pass classified decision notes to the owning agents
//	go -C tools/harness-trace run . merge       merge fragments into ledger.jsonl
//	go -C tools/harness-trace run . verify      check coverage and links (exit 1 on failure)
//	go -C tools/harness-trace run . render      write traceability.md and the matrices
//
// The module is not part of the root go.work, so run it with GOWORK=off.
//
// The tool only reads the upstream checkout. It uses the standard library only.
package main

import (
	"bufio"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// Unit is one upstream thing that must end up with a disposition.
type Unit struct {
	ID    string         `json:"id"`
	Type  string         `json:"type"`
	Name  string         `json:"name,omitempty"`
	Path  string         `json:"path,omitempty"`
	Title string         `json:"title,omitempty"`
	Attrs map[string]any `json:"attrs,omitempty"`
}

// unitTypes lists the unit types in the order they are reported.
var unitTypes = []string{"PKG", "DEP", "DEC", "DEBT", "TOOL", "EVT", "CFG", "SVC", "CLI", "PERSIST"}

type config struct {
	repo     string // this repository
	upstream string // the deepseek-harness checkout
	docs     string // docs/harness
	trace    string // docs/harness/trace
}

func main() {
	fs := flag.NewFlagSet("harness-trace", flag.ExitOnError)
	repo := fs.String("repo", "../..", "root of this repository")
	upstream := fs.String("upstream", "", "deepseek-harness checkout (default: <repo>/../deepseek-harness)")
	partial := fs.Bool("partial", false, "verify: report coverage but do not fail on units without a disposition")
	if len(os.Args) < 2 {
		fmt.Fprintln(os.Stderr, "usage: harness-trace <inventory|assign|handoff|merge|verify|render> [flags]")
		os.Exit(2)
	}
	cmd := os.Args[1]
	_ = fs.Parse(os.Args[2:])

	root, err := filepath.Abs(*repo)
	must(err)
	up := *upstream
	if up == "" {
		up = filepath.Join(root, "..", "deepseek-harness")
	}
	up, err = filepath.Abs(up)
	must(err)
	c := config{repo: root, upstream: up, docs: filepath.Join(root, "docs", "harness")}
	c.trace = filepath.Join(c.docs, "trace")

	switch cmd {
	case "inventory":
		must(runInventory(c))
	case "assign":
		must(runAssign(c))
	case "handoff":
		must(runHandoff(c))
	case "merge":
		_, problems, err := runMerge(c, true)
		must(err)
		printProblems(problems)
	case "verify":
		ok, err := runVerify(c, *partial)
		must(err)
		if !ok {
			os.Exit(1)
		}
	case "render":
		must(runRender(c))
	default:
		fmt.Fprintf(os.Stderr, "unknown command %q\n", cmd)
		os.Exit(2)
	}
}

func must(err error) {
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}

// writeJSONL writes one JSON object per line, in the order given.
func writeJSONL[T any](path string, rows []T) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()
	w := bufio.NewWriter(f)
	enc := json.NewEncoder(w)
	enc.SetEscapeHTML(false)
	for _, r := range rows {
		if err := enc.Encode(r); err != nil {
			return err
		}
	}
	return w.Flush()
}

// readJSONL reads one JSON object per line. Blank lines are skipped. A line that does
// not parse is reported with its file and line number, never dropped silently.
func readJSONL[T any](path string) ([]T, []string, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, nil, err
	}
	defer f.Close()
	var rows []T
	var bad []string
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 1024*1024), 16*1024*1024)
	n := 0
	for sc.Scan() {
		n++
		line := strings.TrimSpace(strings.TrimPrefix(sc.Text(), string([]byte{0xEF, 0xBB, 0xBF})))
		if line == "" {
			continue
		}
		var r T
		if err := json.Unmarshal([]byte(line), &r); err != nil {
			bad = append(bad, fmt.Sprintf("%s:%d: %v", filepath.Base(path), n, err))
			continue
		}
		rows = append(rows, r)
	}
	return rows, bad, sc.Err()
}

// loadInventory reads every inventory file.
func loadInventory(c config) ([]Unit, error) {
	var all []Unit
	for _, t := range unitTypes {
		p := filepath.Join(c.trace, "inventory", strings.ToLower(t)+".jsonl")
		rows, bad, err := readJSONL[Unit](p)
		if err != nil {
			return nil, fmt.Errorf("inventory missing (run inventory first): %w", err)
		}
		if len(bad) > 0 {
			return nil, fmt.Errorf("inventory file is corrupt: %s", bad[0])
		}
		all = append(all, rows...)
	}
	return all, nil
}

func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

func slash(p string) string { return filepath.ToSlash(p) }

func printProblems(problems []string) {
	for _, p := range problems {
		fmt.Println("  -", p)
	}
}
