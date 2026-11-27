package file

import (
	"fmt"

	"github.com/smartpcr/agent-plugin/src/go/cordis"
	"github.com/smartpcr/agent-plugin/src/harness/internal/human/secret"
	"github.com/smartpcr/agent-plugin/src/harness/internal/storage/home"
)

// Name is the catalogue name of Component.
const Name = "human.secrets"

// Config is human.secrets' config: the backend the store keeps its values in.
// Only "file" (the default) is built: the encrypted store under the home's
// secrets directory.
type Config struct {
	Backend string `json:"backend,omitempty"`
}

// Component is human.secrets: the process's secret store over the file backend in
// the launch's home (home.Key), provided under secret.Key. It reads and writes
// only when a holder asks; loading it creates nothing.
var Component = cordis.Define(Name, func(ctx *cordis.Context, cfg Config) error {
	if cfg.Backend != "" && cfg.Backend != "file" {
		return fmt.Errorf("human.secrets: backend %q is not built; the only backend is file", cfg.Backend)
	}
	layout, ok := home.Key.Lookup(ctx)
	if !ok || layout.Root() == "" {
		return fmt.Errorf("human.secrets is mounted without the launch's home (%s)", home.Key.Name())
	}
	_, err := secret.Key.Provide(ctx, secret.NewStore(New(layout.Secrets(), Options{})))
	return err
})
