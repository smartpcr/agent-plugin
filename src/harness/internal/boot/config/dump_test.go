package config_test

import (
	"encoding/json"
	"reflect"
	"testing"

	"github.com/smartpcr/agent-plugin/src/go/loader"
	"github.com/smartpcr/agent-plugin/src/harness/internal/boot/config"
	"github.com/smartpcr/agent-plugin/src/harness/internal/boot/profile"
	"github.com/smartpcr/agent-plugin/src/harness/internal/testkit/golden"
)

const dumpBundle = `- insert:
    - id: probe
      name: x.probe
      config:
        level: info
        port: 1
        tags: []
    - id: quiet
      name: x.probe
      disabled: true
    - id: box
      group: true
      children:
        - id: inner
          name: x.probe
          config:
            level: warn
`

const dumpUser = `- id: probe
  merge:
    level: !expr env.LEVEL
    nested:
      deep: true
- id: quiet
  disabled: !expr 'env.QUIET == "1"'
  inject: [svc.a]
  isolate:
    svc.a: true
    svc.b: realm
  intercept:
    svc.c:
      mode: !expr env.MODE
- id: box:inner
  merge:
    level: error
`

// The YAML dump is one loadable layer: annotated with the supplying layers,
// expressions in source form, and loaded as the only layer it composes to the
// same tree (DH-D-BOOT-6).
//
//dsh:test DH-T-BOOT-14
func TestDumpYAMLRoundTrips(t *testing.T) {
	bundle, _ := layer(t, "bundle:core", "core.yml", dumpBundle)
	user, _ := layer(t, "user", "user.yml", dumpUser)
	layers := []loader.Layer{bundle, user}
	r := config.Preview(layers, config.CheckOptions{})
	out, err := r.DumpYAML()
	if err != nil {
		t.Fatal(err)
	}
	golden.Assert(t, "dump", out)

	dumped, issues, err := profile.ParseLayer("dump", "dump.yml", out)
	if err != nil || len(issues) > 0 {
		t.Fatalf("the dump does not load: %v %v\n%s", err, issues, out)
	}
	again, _, cissues := loader.Compose([]loader.Layer{dumped})
	if len(cissues) > 0 {
		t.Fatalf("the dump composes with issues: %v", cissues)
	}
	want, _, _ := loader.Compose(layers)
	if a, b := treeJSON(t, want), treeJSON(t, again); !reflect.DeepEqual(a, b) {
		t.Errorf("the dump composes to another tree:\nwant %v\ngot  %v", a, b)
	}
}

func treeJSON(t *testing.T, tree []loader.EntryOptions) any {
	t.Helper()
	raw, err := json.Marshal(tree)
	if err != nil {
		t.Fatal(err)
	}
	var v any
	if err := json.Unmarshal(raw, &v); err != nil {
		t.Fatal(err)
	}
	return v
}
