//go:build testhook

package builtin

import (
	"net/http"
	"reflect"

	"github.com/smartpcr/agent-plugin/src/go/cordis"
	"github.com/smartpcr/agent-plugin/src/harness/internal/boot/catalog"
	"github.com/smartpcr/agent-plugin/src/harness/internal/llm"
	"github.com/smartpcr/agent-plugin/src/harness/internal/llm/vocab"
	"github.com/smartpcr/agent-plugin/src/harness/internal/testkit/model"
)

// TestRouteName is the catalogue name of the keyless fake model route a testhook
// build adds, so a built-binary test layer can insert it. No release build
// carries the tag.
const TestRouteName = "llm.testkit-route"

// TestRouteConfig is the route item's configuration: the route id and the
// loopback URL serving the kit's SSE dialect. HoldStop makes the item's release
// never return, so a stop runs into the launch's shutdown bound (status 124).
type TestRouteConfig struct {
	ID       string `json:"id"`
	URL      string `json:"url"`
	HoldStop bool   `json:"holdStop,omitempty"`
}

var testRoute = cordis.Define(TestRouteName, func(ctx *cordis.Context, cfg TestRouteConfig) error {
	rt, err := llm.RuntimeKey.Get(ctx)
	if err != nil {
		return err
	}
	_, err = ctx.Effect("llm.testkit-route.register", func() (cordis.Disposer, error) {
		reg, err := rt.Register(TestRouteName, llm.Route{
			ID:      vocab.RouteID(cfg.ID),
			Models:  []llm.Model{{Name: model.ModelName}},
			Adapter: &model.Adapter{Client: &http.Client{}, URL: cfg.URL},
		})
		if err != nil {
			return nil, err
		}
		return func() error {
			reg.Close()
			if cfg.HoldStop {
				select {}
			}
			return nil
		}, nil
	})
	return err
}, cordis.WithInject(llm.RuntimeKey.Name()))

func init() {
	extra = append(extra, catalog.Item{Name: TestRouteName, Component: testRoute, Config: reflect.TypeFor[TestRouteConfig]()})
}
