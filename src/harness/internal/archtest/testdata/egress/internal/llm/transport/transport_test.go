package transport

import "net/http"

// Test files are exempt: not reported.
var _ = Over(&http.Transport{}, Options{})
