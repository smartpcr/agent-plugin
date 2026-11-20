package cordis

import (
	"fmt"
	"strings"
	"testing"
	"time"
)

// A barrier is not held back by a notification posted after its target, even one that
// is still running once the target has returned.
func TestBarrierIgnoresNotificationsPastItsTarget(t *testing.T) {
	tr := New().t
	started, gate := make(chan struct{}), make(chan struct{})
	defer close(gate)
	target := tr.post(func() {})
	tr.post(func() { close(started); <-gate })
	tr.flush()
	select {
	case <-started:
	case <-time.After(5 * time.Second):
		t.Fatal("the later notification did not start")
	}
	done := make(chan struct{})
	go func() { tr.barrier(target-1, target); close(done) }()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("the barrier waited for a notification past its target")
	}
}

func TestParseGoroutineIDAcceptsOnlyAWellFormedHeader(t *testing.T) {
	for header, want := range map[string]uint64{
		"goroutine 1 [running]:\n":      1,
		"goroutine 4711 [chan receive]": 4711,
	} {
		id, ok := parseGoroutineID([]byte(header))
		if !ok || id != want {
			t.Errorf("%q: got %d, %v; want %d", header, id, ok, want)
		}
	}
	for _, header := range []string{
		"",
		"goroutine ",
		"goroutine [running]",
		"goroutine 0 [running]",
		"goroutine -1 [running]",
		"goroutine +1 [running]",
		"goroutine x1 [running]",
		"goroutine 12",
		"goroutine 18446744073709551616 [running]",
		"Goroutine 1 [running]",
		"thread 1 [running]",
	} {
		if id, ok := parseGoroutineID([]byte(header)); ok {
			t.Errorf("%q: accepted as %d", header, id)
		}
	}
}

func TestGoroutineIDIsDistinctPerGoroutine(t *testing.T) {
	mine, ok := goroutineID()
	if !ok || mine == 0 {
		t.Fatalf("own id: %d, %v", mine, ok)
	}
	other := make(chan uint64)
	go func() {
		id, ok := goroutineID()
		if !ok {
			id = 0
		}
		other <- id
	}()
	if id := <-other; id == 0 || id == mine {
		t.Fatalf("other goroutine's id %d, own %d", id, mine)
	}
}

// quietTree returns a tree whose log output goes to the returned exporter only.
func quietTree() (*tree, *MemoryExporter) {
	root := New()
	root.Loggers().ClearExporters()
	logs := &MemoryExporter{}
	root.Loggers().AddExporter(logs)
	return root.t, logs
}

func setGoroutineID(t *testing.T, fn func() (uint64, bool)) {
	saved := goroutineID
	goroutineID = fn
	t.Cleanup(func() { goroutineID = saved })
}

// Without a goroutine identity a delivery is not registered, so a park attributes
// nothing; notifications are still delivered, in order, and the failure is logged once.
func TestUnidentifiedDeliveryIsNeverParked(t *testing.T) {
	setGoroutineID(t, func() (uint64, bool) { return 0, false })
	tr, logs := quietTree()
	var order []int
	var registered, parked int
	for i := range 3 {
		tr.post(func() {
			unpark := tr.park()
			tr.outboxMu.Lock()
			registered += len(tr.byGoroutine)
			for _, d := range tr.running {
				parked += d.parked
			}
			tr.outboxMu.Unlock()
			unpark()
			order = append(order, i)
		})
	}
	tr.flush()
	tr.barrierReturned(0, tr.mark())
	if registered != 0 || parked != 0 {
		t.Fatalf("registered %d, parked %d deliveries without an identity", registered, parked)
	}
	if fmt.Sprint(order) != "[0 1 2]" {
		t.Fatalf("delivered in order %v", order)
	}
	warned := 0
	for _, m := range logs.Messages() {
		if m.Level == LevelWarn && strings.Contains(m.Text, "cannot identify the goroutine") {
			warned++
		}
	}
	if warned != 1 {
		t.Fatalf("logged the missing identity %d times", warned)
	}
}

// A delivery whose identity is already registered to another delivery does not take it
// over, so a park on that identity keeps attributing to the first.
func TestDuplicateGoroutineIdentityIsNotShared(t *testing.T) {
	setGoroutineID(t, func() (uint64, bool) { return 7, true })
	tr, _ := quietTree()
	gate := make(chan struct{})
	var first *delivery
	var owner *delivery
	tr.post(func() {
		tr.outboxMu.Lock()
		first = tr.byGoroutine[7]
		tr.outboxMu.Unlock()
		tr.await(gate)
	})
	second := make(chan struct{})
	tr.post(func() {
		tr.outboxMu.Lock()
		owner = tr.byGoroutine[7]
		tr.outboxMu.Unlock()
		close(second)
	})
	tr.flush()
	select {
	case <-second:
	case <-time.After(5 * time.Second):
		t.Fatal("the parked first delivery held back the second")
	}
	close(gate)
	tr.barrierReturned(0, tr.mark())
	if first == nil || owner != first {
		t.Fatalf("identity owned by %p during the second delivery; first %p", owner, first)
	}
}
