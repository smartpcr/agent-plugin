package jsonrpc

import "testing"

// decodeSeeds are the fuzz seeds of FuzzDecode, compiled into the test binary so a
// plain go test runs them with no file read (QG-6). TestDecodeSeeds feeds the same
// slice to the decoder directly.
var decodeSeeds = [][]byte{
	[]byte(`{"jsonrpc":"2.0","id":1,"method":"session/list"}`),
	[]byte(`{"jsonrpc":"2.0","id":"a","method":"initialize","params":{"protocolVersions":[1]}}`),
	[]byte(`{"jsonrpc":"2.0","method":"session.event","params":[1,2,3]}`),
	[]byte(`{"jsonrpc":"2.0","method":"$/cancel","params":{"id":7}}`),
	[]byte(`{"jsonrpc":"2.0","id":1,"result":{"ok":true}}`),
	[]byte(`{"jsonrpc":"2.0","id":2,"error":{"code":-32000,"message":"busy","data":{"code":"jsonrpc/busy"}}}`),
	[]byte(`{"jsonrpc":"2.0","id":null,"error":{"code":-32700,"message":"parse error"}}`),
	[]byte(`{"jsonrpc":"2.0","id":3,"method":"x","result":1}`),
	[]byte(`{"jsonrpc":"2.0","id":1,"id":2,"method":"x"}`),
	[]byte(`{"jsonrpc":"1.0","id":1,"method":"x"}`),
	[]byte(`{"jsonrpc":"2.0","id":1.5,"method":"x"}`),
	[]byte(`{"jsonrpc":"2.0","id":1,"method":"x","params":"no"}`),
	[]byte(`{"jsonrpc":"2.0","id":1,"method":"x","extra":true}`),
	[]byte(`{"jsonrpc":"2.0","id":1,"error":{"code":1,"message":"m","data":{"code":"Bad Reason"}}}`),
	[]byte(`[{"jsonrpc":"2.0","id":1,"method":"x"}]`),
	[]byte(`{"jsonrpc":"2.0","id":1,"method":"x"} {}`),
	[]byte(`{"jsonrpc":"2.0","id":99999999999999999999,"method":"x"}`),
	[]byte("{\"jsonrpc\":\"2.0\",\"id\":\"\xff\",\"method\":\"x\"}\r"),
	[]byte(`"just a string"`),
	[]byte(`{`),
	[]byte(``),
	nil,
}

func FuzzDecode(f *testing.F) {
	for _, b := range decodeSeeds {
		f.Add(b)
	}
	f.Fuzz(func(t *testing.T, b []byte) { checkDecode(t, b) })
}
