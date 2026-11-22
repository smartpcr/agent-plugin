package tools

import (
	"strings"
	"testing"
	"time"
)

type node struct {
	Name string
	Next *node
	Tags map[string][]string
	Any  any
}

func TestIsolateCopiesDeeplyAndKeepsCycles(t *testing.T) {
	n := &node{Name: "a", Tags: map[string][]string{"k": {"v"}}, Any: []int{1}}
	n.Next = n
	v, err := isolate(n)
	if err != nil {
		t.Fatal(err)
	}
	c := v.(*node)
	if c == n || c.Next != c {
		t.Fatalf("copy %p next %p, original %p: want a distinct node pointing at itself", c, c.Next, n)
	}
	c.Tags["k"][0], c.Any.([]int)[0] = "x", 9
	if n.Tags["k"][0] != "v" || n.Any.([]int)[0] != 1 {
		t.Fatalf("mutating the copy changed the original: %+v", n)
	}
}

type private struct {
	tags []string
	at   time.Time
	fn   func()
}

func TestIsolateCopiesPrivateState(t *testing.T) {
	now := time.Now()
	p := private{tags: []string{"v"}, at: now}
	v, err := isolate(p)
	if err != nil {
		t.Fatal(err)
	}
	c := v.(private)
	c.tags[0] = "x"
	if p.tags[0] != "v" || !c.at.Equal(now) || c.at.Location() != now.Location() {
		t.Fatalf("copy %+v of %+v shares private state or lost the time", c, p)
	}
	if _, err := isolate(private{fn: func() {}}); err == nil || !strings.Contains(err.Error(), ".fn") {
		t.Fatalf("err %v, want a refusal naming the private .fn", err)
	}
}

func TestIsolateRefusesWhatCannotBeCopied(t *testing.T) {
	_, err := isolate(node{Any: map[string]any{"f": func() {}}})
	if err == nil || !strings.Contains(err.Error(), ".Any[f]") || !strings.Contains(err.Error(), "func") {
		t.Fatalf("err %v, want a refusal naming .Any[f]", err)
	}
}
