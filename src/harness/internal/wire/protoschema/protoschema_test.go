package protoschema

import (
	"reflect"
	"strings"
	"testing"

	"github.com/smartpcr/agent-plugin/src/sdk/protocol"
)

// TestEveryProtocolTypeDerives is the drift test of M1-D-FND-1 (a): every
// parameter and result type of protocol.Methods derives through schema.ForType
// without error, so the SDK's own deriver and the harness's schema package agree
// on what the protocol types are.
//
//dsh:test DH-T-SDK-5
func TestEveryProtocolTypeDerives(t *testing.T) {
	types := Types(protocol.Methods)
	if len(types) == 0 {
		t.Fatal("protocol.Methods has no types")
	}
	for _, m := range protocol.Methods {
		for _, typ := range []reflect.Type{m.Params, m.Result} {
			if typ != nil && !contains(types, typ) {
				t.Errorf("%s: type %s is not listed", m.Name, typ)
			}
		}
	}
	if err := Check(protocol.Methods); err != nil {
		t.Fatal(err)
	}
}

// TestCheckReportsEveryFailure: a type schema.ForType refuses fails the check,
// naming the type and the methods that use it.
//
//dsh:test DH-T-SDK-5
func TestCheckReportsEveryFailure(t *testing.T) {
	bad := reflect.TypeFor[chan int]()
	methods := []protocol.Method{
		{Name: "a", Kind: protocol.Request, Params: bad, Result: reflect.TypeFor[protocol.Empty]()},
		{Name: "b.note", Kind: protocol.Notification, Params: bad},
	}
	if got := Types(methods); len(got) != 2 {
		t.Fatalf("Types = %v, want the two distinct types", got)
	}
	err := Check(methods)
	if err == nil || !strings.Contains(err.Error(), "chan int (used by [a b.note])") {
		t.Fatalf("Check = %v", err)
	}
}

func contains(types []reflect.Type, t reflect.Type) bool {
	for _, x := range types {
		if x == t {
			return true
		}
	}
	return false
}
