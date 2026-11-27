package listen_test

import (
	"reflect"
	"testing"

	"github.com/smartpcr/agent-plugin/src/harness/internal/host/listen"
	"github.com/smartpcr/agent-plugin/src/harness/internal/util/herr"
)

// web's --bind and --port become host.listen's config patch; a missing or bad
// value and the upstream --host are listen/flag.
//
//dsh:test DH-T-CLI-7
func TestAddressFlags(t *testing.T) {
	for _, c := range []struct {
		words []string
		want  map[string]any
	}{
		{nil, nil},
		{[]string{"--no-open", "--tls-cert", "c"}, nil},
		{[]string{"--port", "0"}, map[string]any{"port": uint64(0)}},
		{[]string{"--port=4000", "--bind", "::1", "--port", "4001"}, map[string]any{"port": uint64(4001), "bind": "::1"}},
		{[]string{"--", "--port", "x"}, nil},
	} {
		got, err := listen.AddressFlags(c.words)
		if err != nil || !reflect.DeepEqual(got, c.want) {
			t.Errorf("AddressFlags(%q) = %v %v, want %v", c.words, got, err, c.want)
		}
	}
	for _, words := range [][]string{{"--port"}, {"--port", "--no-open"}, {"--port", "70000"}, {"--port=-1"}, {"--bind="}, {"--host", "0.0.0.0"}} {
		if _, err := listen.AddressFlags(words); herr.Kind(err) != listen.ReasonFlag {
			t.Errorf("AddressFlags(%q) = %v, want %s", words, err, listen.ReasonFlag)
		}
	}
}
