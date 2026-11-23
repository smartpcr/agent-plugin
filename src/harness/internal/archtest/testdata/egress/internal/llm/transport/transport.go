package transport

import (
	"net/http"

	"github.com/smartpcr/agent-plugin/src/harness/internal/util/egress"
)

type Options struct{}

type Transport struct{ client *http.Client }

// Over may use egress.Over: not reported.
func Over(rt http.RoundTripper, _ Options) *Transport { return &Transport{client: egress.Over(rt)} }

// A production path of the seam's own package that calls the seam.
func Default() *Transport { return Over(nil, Options{}) } // seeded: own-package seam call
