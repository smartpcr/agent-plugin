package secret

import (
	"testing"

	"github.com/smartpcr/agent-plugin/src/harness/internal/world/subprocess"
)

// Without a store, BuildEnv's default deny patterns still remove every name this
// package treats as credential-shaped, so the two lists cannot drift apart.
//
//dsh:test DH-T-EXEC-3
func TestDefaultDenyCoversEveryCredentialShape(t *testing.T) {
	for _, suf := range secretSuffixes {
		for _, name := range []string{"ACME" + suf, suf[1:]} {
			if !SecretShaped(name) {
				t.Fatalf("%s is not credential-shaped", name)
			}
			if env := subprocess.BuildEnv([]string{name + "=v"}, subprocess.EnvOptions{}); env.Has(name) {
				t.Errorf("BuildEnv with no store keeps the credential-shaped %s", name)
			}
		}
	}
}
