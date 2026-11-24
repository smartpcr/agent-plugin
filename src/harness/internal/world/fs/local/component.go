package local

import (
	"github.com/smartpcr/agent-plugin/src/go/cordis"
	"github.com/smartpcr/agent-plugin/src/harness/internal/world/fs"
)

// Name is the component's catalogue name.
const Name = "fs.local"

// Component is fs.local (C32, cycle C-3): the local provider configured by its
// Options, provided as the filesystem seam under fs.Key. The working directory
// validates through it and fs/ops fences every call to it.
var Component = cordis.Define(Name, func(ctx *cordis.Context, o Options) error {
	_, err := fs.Key.Provide(ctx, NewWithOptions(o))
	return err
})
