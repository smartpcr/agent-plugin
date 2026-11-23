package protocol

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"

	"github.com/smartpcr/agent-plugin/src/sdk/jsonrpc"
)

// Registration is one typed handler for one request of the method table, made by
// Handle and checked by Register.
type Registration struct {
	Name    string
	Params  reflect.Type
	Result  reflect.Type
	Handler jsonrpc.Handler
}

// Handle registers fn for request name: the params are decoded strictly with
// DecodeParams[P] (a failure is jsonrpc/invalid-params and fn is not called), and
// fn's result is the reply.
func Handle[P, R any](name string, fn func(context.Context, P) (R, error)) Registration {
	return Registration{
		Name:   name,
		Params: reflect.TypeFor[P](),
		Result: reflect.TypeFor[R](),
		Handler: func(ctx context.Context, raw json.RawMessage) (any, error) {
			p, err := DecodeParams[P](raw)
			if err != nil {
				return nil, err
			}
			return fn(ctx, p)
		},
	}
}

// Requests returns the request rows of Methods that from sends: the rows a peer
// receiving them must register.
func Requests(from Direction) []Method {
	var rows []Method
	for _, m := range Methods {
		if m.Kind == Request && (m.Direction == from || m.Direction == Both) {
			rows = append(rows, m)
		}
	}
	return rows
}

// Register checks regs against the request rows of table both ways and returns
// the handler map for jsonrpc.Options.Methods. Every request row needs exactly one
// registration whose parameter and result types are the row's; a registration for
// a name with no request row, a second registration of a name, a type mismatch and
// a row without a registration each fail, naming the method. Every failure is
// reported, joined, and no map is returned.
func Register(table []Method, regs ...Registration) (map[string]jsonrpc.Handler, error) {
	rows := map[string]Method{}
	for _, m := range table {
		if m.Kind == Request {
			rows[m.Name] = m
		}
	}
	var errs []error
	named := map[string]bool{}
	out := make(map[string]jsonrpc.Handler, len(regs))
	for _, r := range regs {
		m, ok := rows[r.Name]
		switch {
		case !ok:
			errs = append(errs, fmt.Errorf("protocol: %q is registered but is not a request of the method table", r.Name))
		case named[r.Name]:
			errs = append(errs, fmt.Errorf("protocol: %q is registered twice", r.Name))
		case r.Params != m.Params || r.Result != m.Result:
			errs = append(errs, fmt.Errorf("protocol: %q is registered as %v -> %v; the method table says %v -> %v", r.Name, r.Params, r.Result, m.Params, m.Result))
		case r.Handler == nil:
			errs = append(errs, fmt.Errorf("protocol: %q is registered without a handler", r.Name))
		default:
			out[r.Name] = r.Handler
		}
		named[r.Name] = true
	}
	for _, m := range table {
		if m.Kind == Request && !named[m.Name] {
			errs = append(errs, fmt.Errorf("protocol: request %q of the method table has no handler", m.Name))
		}
	}
	if len(errs) > 0 {
		return nil, errors.Join(errs...)
	}
	return out, nil
}
