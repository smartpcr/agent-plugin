package sender

import (
	"net/http"
	"time"
)

// A reference to a client somebody else built is not a client: not reported.
type Sender struct {
	c  *http.Client
	rt *http.Transport
}

func New(c *http.Client) *Sender { return &Sender{c: c} }

func (s *Sender) Send(req *http.Request) (*http.Response, error) { return s.c.Do(req) }

var built = &http.Client{Timeout: time.Second} // seeded: client literal

var plain = http.Client{} // seeded: client literal without &

var made = new(http.Transport) // seeded: new(http.Transport)

var zero http.Client // seeded: zero client variable

type holder struct {
	t http.Transport // seeded: transport field
}

func defaults() {
	_ = http.DefaultClient                                // seeded: default client
	_ = http.DefaultTransport                             // seeded: default transport
	_, _ = http.Get("https://example.invalid/")           // seeded: http.Get
	_, _ = http.Head("https://example.invalid/")          // seeded: http.Head
	_, _ = http.Post("https://example.invalid/", "", nil) // seeded: http.Post
	_, _ = http.PostForm("https://example.invalid/", nil) // seeded: http.PostForm
}
