// Package cordis is the rootcall fixture's stand-in for src/go/cordis: inside it,
// Root may be used.
package cordis

// Context is a node of the context tree.
type Context struct{ root *Context }

// Root is the accessor the rootcall analyzer restricts.
func (c *Context) Root() *Context { return c.root }

// Parent is another method, never reported.
func (c *Context) Parent() *Context { return c }

func inside(c *Context) *Context { return c.Root() }
