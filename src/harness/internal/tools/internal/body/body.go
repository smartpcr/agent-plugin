// Package body is the seat through which the packages under internal/tools reach
// a declaration's argument decoder and body. Go's internal rule admits only
// internal/tools and the packages below it, so no other package can run a tool
// body: the pipeline is the one way in (areas/tools.md §6.1).
package body

import (
	"context"
	"encoding/json"
)

// Body is one declaration's decoder and body, as package tools fills it.
type Body struct {
	// Decode strictly decodes the raw argument bytes into the input type and runs
	// its Validate; the error is a *tools.Error with every violation.
	Decode func(args json.RawMessage) (any, error)
	// Run calls the body with a value Decode returned and a tools.Run.
	Run func(ctx context.Context, in any, run any) (any, error)
}

// Of returns the body of a *tools.Declaration; package tools sets it at init.
var Of func(decl any) (Body, bool)
