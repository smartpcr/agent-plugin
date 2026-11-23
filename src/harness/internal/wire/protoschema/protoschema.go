// Package protoschema keeps the SDK protocol's own schema derivation and the
// harness's schema package from drifting apart (M1-D-FND-1 (a), plan step 14.3.3).
// src/sdk may not import the harness (RM-3), so src/sdk/protocol derives its
// committed schema.json with a small deriver of its own; this package checks that
// every parameter and result type of protocol.Methods also derives without error
// through internal/schema (schema.ForType), the package every other harness surface
// uses. The SDK spec names this file at DH-S-15.
package protoschema

import (
	"errors"
	"fmt"
	"reflect"

	"github.com/smartpcr/agent-plugin/src/harness/internal/schema"
	"github.com/smartpcr/agent-plugin/src/sdk/protocol"
)

// Types returns every parameter and result type of methods, once each, in table
// order.
func Types(methods []protocol.Method) []reflect.Type {
	seen := map[reflect.Type]bool{}
	var out []reflect.Type
	for _, m := range methods {
		for _, t := range []reflect.Type{m.Params, m.Result} {
			if t != nil && !seen[t] {
				seen[t] = true
				out = append(out, t)
			}
		}
	}
	return out
}

// Check derives every type of methods with schema.ForType and returns every
// failure, joined, each naming the methods that use the type.
func Check(methods []protocol.Method) error {
	users := map[reflect.Type][]string{}
	for _, m := range methods {
		for _, t := range []reflect.Type{m.Params, m.Result} {
			if t != nil {
				users[t] = append(users[t], m.Name)
			}
		}
	}
	var errs []error
	for _, t := range Types(methods) {
		if _, err := schema.ForType(t); err != nil {
			errs = append(errs, fmt.Errorf("protoschema: %s (used by %v): %w", t, users[t], err))
		}
	}
	return errors.Join(errs...)
}
