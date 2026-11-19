package cleanup

import (
	"net"
	"net/http"
	"net/http/httptest"
	"os/exec"
	"sync"
	"testing"
)

func TestGoroutineLeaks(t *testing.T) {
	go func() {}() // want `goroutine started in a test without registering its release with t.Cleanup`
}

func TestGoroutineReleased(t *testing.T) {
	var wg sync.WaitGroup
	t.Cleanup(wg.Wait)
	wg.Add(1)
	go func() { defer wg.Done() }()
}

func TestListenerLeaks(t *testing.T) {
	l, _ := net.Listen("tcp", "127.0.0.1:0") // want `listener started in a test without registering its release with t.Cleanup`
	_ = l
}

func TestServerReleased(t *testing.T) {
	srv := httptest.NewServer(http.NotFoundHandler())
	t.Cleanup(srv.Close)
}

func TestChildLeaks(t *testing.T) {
	cmd := exec.Command("go", "version")
	_ = cmd.Start() // want `child process started in a test without registering its release with t.Cleanup`
}

func TestChildRunsToCompletion(t *testing.T) {
	_ = exec.Command("go", "version").Run()
}

func TestSubtests(t *testing.T) {
	t.Run("leaks", func(t *testing.T) {
		go func() {}() // want `goroutine started in a test without registering its release with t.Cleanup`
	})
	t.Run("released", func(t *testing.T) {
		done := make(chan struct{})
		t.Cleanup(func() { <-done })
		go func() { close(done) }()
	})
}

func startHelper(tb testing.TB) {
	tb.Helper()
	go func() {}() // want `goroutine started in a test without registering its release with t.Cleanup`
}

func TestOtherReceiverDoesNotCount(t *testing.T) {
	startHelper(t)
	other := &testing.T{}
	other.Cleanup(func() {})
	go func() {}() // want `goroutine started in a test without registering its release with t.Cleanup`
}

func BenchmarkReleased(b *testing.B) {
	done := make(chan struct{})
	b.Cleanup(func() { <-done })
	go close(done)
}
