// Package rootcall seeds the rootcall analyzer.
package rootcall

import "github.com/smartpcr/agent-plugin/src/go/cordis"

// Root is another type with a Root method, never reported.
type Root struct{}

func (Root) Root() {}

func cases(c *cordis.Context) {
	_ = c.Root()                // want `\(\*cordis.Context\).Root used outside cordis`
	f := c.Root                 // want `\(\*cordis.Context\).Root used outside cordis`
	g := (*cordis.Context).Root // want `\(\*cordis.Context\).Root used outside cordis`
	_, _ = f, g
	_ = c.Parent()
	Root{}.Root()

	//lint:ignore rootcall the fixture's reasoned exception
	_ = c.Root()
}
