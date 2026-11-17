// Package testkit holds the fixtures and helpers shared by the cordis test suites.
package testkit

import (
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/smartpcr/agent-plugin/src/go/cordis"
)

// Timeout bounds every blocking step of a test, so a deadlock fails instead of hanging.
const Timeout = 5 * time.Second

// Within runs fn and fails the test if it does not return within Timeout.
func Within(t testing.TB, what string, fn func()) {
	t.Helper()
	done := make(chan struct{})
	go func() {
		defer close(done)
		fn()
	}()
	select {
	case <-done:
	case <-time.After(Timeout):
		t.Fatalf("%s did not finish within %s", what, Timeout)
	}
}

// Wait waits for fiber to settle and returns its load error.
func Wait(t testing.TB, fiber *cordis.Fiber) error {
	t.Helper()
	var err error
	Within(t, "fiber "+fiber.Name(), func() { err = fiber.Wait() })
	return err
}

// MustWait waits for fiber to settle and fails the test on a load error.
func MustWait(t testing.TB, fiber *cordis.Fiber) *cordis.Fiber {
	t.Helper()
	if err := Wait(t, fiber); err != nil {
		t.Fatalf("fiber %s failed: %v", fiber.Name(), err)
	}
	return fiber
}

// Must returns value, panicking on err. (A multi-value call cannot share an argument
// list with t, so this helper reports through the test's panic instead.)
func Must[T any](value T, err error) T {
	if err != nil {
		panic(fmt.Sprintf("unexpected error: %v", err))
	}
	return value
}

// Eventually polls condition until it holds, failing the test after Timeout.
func Eventually(t testing.TB, condition func() bool, message string) {
	t.Helper()
	deadline := time.Now().Add(Timeout)
	for !condition() {
		if time.Now().After(deadline) {
			t.Fatal(message)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// Equal fails the test when got differs from want.
func Equal[T comparable](t testing.TB, want, got T, context ...string) {
	t.Helper()
	if want != got {
		t.Fatalf("%s: want %v, got %v", strings.Join(context, " "), want, got)
	}
}

// True fails the test with message when condition is false.
func True(t testing.TB, condition bool, message string) {
	t.Helper()
	if !condition {
		t.Fatal(message)
	}
}

// Quiet returns a root context whose log output goes to a MemoryExporter only.
func Quiet() (*cordis.Context, *cordis.MemoryExporter) {
	root := cordis.New()
	root.Loggers().ClearExporters()
	logs := &cordis.MemoryExporter{}
	root.Loggers().AddExporter(logs)
	return root, logs
}

// Count returns the number of messages at level.
func Count(logs *cordis.MemoryExporter, level cordis.LogLevel) int {
	count := 0
	for _, message := range logs.Messages() {
		if message.Level == level {
			count++
		}
	}
	return count
}

// Texts returns the text of every message at level.
func Texts(logs *cordis.MemoryExporter, level cordis.LogLevel) []string {
	var texts []string
	for _, message := range logs.Messages() {
		if message.Level == level {
			texts = append(texts, message.Text)
		}
	}
	return texts
}

// Log is a thread-safe list of lines.
type Log struct {
	mu    sync.Mutex
	lines []string
}

// Add appends a line.
func (l *Log) Add(line string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.lines = append(l.lines, line)
}

// Lines returns a snapshot.
func (l *Log) Lines() []string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return slices.Clone(l.lines)
}

// String joins the lines with commas.
func (l *Log) String() string { return strings.Join(l.Lines(), ",") }

// Contains reports whether line was added.
func (l *Log) Contains(line string) bool { return slices.Contains(l.Lines(), line) }

// Count returns how many times line was added.
func (l *Log) Count(line string) int {
	count := 0
	for _, candidate := range l.Lines() {
		if candidate == line {
			count++
		}
	}
	return count
}

// ------------------------------------------------------------------ fixtures

// EchoOptions is the config of Echo.
type EchoOptions struct {
	Times int    `json:"times"`
	Text  string `json:"text"`
}

// Validate requires Times in [1, 10].
func (o EchoOptions) Validate() error {
	if o.Times < 1 || o.Times > 10 {
		return &cordis.ConfigError{Issues: []cordis.ConfigIssue{{Message: "must be between 1 and 10", Path: "times"}}}
	}
	return nil
}

// Echo returns a component that logs "+line" on load and "-line" on unload, where
// line is Text repeated Times times.
func Echo(log *Log) *cordis.Component {
	return cordis.Define("EchoPlugin", func(ctx *cordis.Context, config EchoOptions) error {
		_, err := ctx.Effect("echo", func() (cordis.Disposer, error) {
			line := strings.TrimSpace(strings.Repeat(config.Text+" ", config.Times))
			log.Add("+" + line)
			return func() error {
				log.Add("-" + line)
				return nil
			}, nil
		})
		return err
	}, cordis.WithDefaults(func() EchoOptions { return EchoOptions{Times: 1} }))
}

// CounterService is the "counter" service.
type CounterService struct {
	mu    sync.Mutex
	value int
}

// Next increments and returns the counter.
func (c *CounterService) Next() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.value++
	return c.value
}

// CounterKey is the typed key of the "counter" service.
var CounterKey = cordis.NewServiceKey[*CounterService]("counter")

// Counter provides CounterService under "counter".
var Counter = cordis.Construct("CounterService", func(ctx *cordis.Context, _ cordis.None) (*CounterService, error) {
	service := &CounterService{}
	if _, err := CounterKey.Provide(ctx, service); err != nil {
		return nil, err
	}
	return service, nil
})

// ErrBoom is the failure of Failing.
var ErrBoom = errors.New("boom")

// Failing always fails to load.
var Failing = cordis.Define("FailingComponent", func(*cordis.Context, cordis.None) error { return ErrBoom })

// NeedsCounter loads only while "counter" is available.
var NeedsCounter = cordis.Define("NeedsCounter", func(*cordis.Context, cordis.None) error { return nil },
	cordis.WithInject("counter"))

// NoError fails the test on err.
func NoError(t testing.TB, err error) {
	t.Helper()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}
