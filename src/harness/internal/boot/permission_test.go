package boot

import (
	"context"
	"strings"
	"testing"

	"github.com/smartpcr/agent-plugin/src/harness/internal/boot/builtin"
	"github.com/smartpcr/agent-plugin/src/harness/internal/cli/output"
	"github.com/smartpcr/agent-plugin/src/harness/internal/testkit"
)

// TC-02a, DH-D-HUMAN-4: a layer whose default permission preset names no entry
// of the table fails the configuration load with status 78 and one fatal message
// naming the preset at its field.
func TestStartupRejectsAnUnresolvableDefaultPermissionPreset(t *testing.T) {
	opts, _ := fixture(t, `- insert:
    - id: permissions
      name: permission.presets
      config:
        defaultPreset: ghost
`)
	cat, err := builtin.Catalog()
	if err != nil {
		t.Fatal(err)
	}
	var stderr testkit.Buffer
	opts.Catalog, opts.Stderr = cat, &stderr
	if status := Run(context.Background(), opts); status != output.Config {
		t.Errorf("status = %d, want %d", status, output.Config)
	}
	got := stderr.String()
	if n := strings.Count(got, "dsh: fatal:"); n != 1 {
		t.Fatalf("%d fatal messages, want 1:\n%s", n, got)
	}
	if !strings.Contains(got, "permissions.config.defaultPreset") || !strings.Contains(got, `default permission preset "ghost" is not a preset of the table`) {
		t.Errorf("the message does not name the preset at its field:\n%s", got)
	}
}
