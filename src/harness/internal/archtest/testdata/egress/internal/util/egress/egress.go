package egress

import "net/http"

// The owner builds clients: not reported.
func Client(rt http.RoundTripper) *http.Client { return &http.Client{Transport: rt} }

// Over is the bare-client seam; defining it is not a use.
func Over(rt http.RoundTripper) *http.Client { return &http.Client{Transport: rt} }

var _ = &http.Transport{}
