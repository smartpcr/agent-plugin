package cleanup

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

// countingServer embeds the server it starts, as a test fixture often does: the
// listener is assigned to a field, and the cleanup releases it through the struct
// (M1-G-GATE-5; DH-T-TOOLCHAIN-8).
type countingServer struct {
	*httptest.Server
	requests int
}

func TestFieldServerReleased(t *testing.T) {
	cs := &countingServer{}
	cs.Server = httptest.NewServer(http.NotFoundHandler())
	t.Cleanup(cs.Close)
}

func TestNestedFieldServerReleased(t *testing.T) {
	var w struct{ cs countingServer }
	w.cs.Server = httptest.NewServer(http.NotFoundHandler())
	t.Cleanup(func() { w.cs.Close() })
}

func TestFieldServerLeaks(t *testing.T) {
	cs := &countingServer{}
	cs.Server = httptest.NewServer(http.NotFoundHandler()) // want `listener started in a test without registering its release with t.Cleanup`
}

func TestFieldServerOtherCleanupDoesNotCount(t *testing.T) {
	cs := &countingServer{}
	other := httptest.NewServer(http.NotFoundHandler())
	t.Cleanup(other.Close)
	cs.Server = httptest.NewServer(http.NotFoundHandler()) // want `listener started in a test without registering its release with t.Cleanup`
}
