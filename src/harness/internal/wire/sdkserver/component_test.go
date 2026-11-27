package sdkserver_test

import (
	"context"
	"errors"
	"io"
	"testing"
	"time"

	"github.com/smartpcr/agent-plugin/src/go/cordis"
	"github.com/smartpcr/agent-plugin/src/harness/internal/agent"
	"github.com/smartpcr/agent-plugin/src/harness/internal/boot/launch"
	"github.com/smartpcr/agent-plugin/src/harness/internal/llm"
	"github.com/smartpcr/agent-plugin/src/harness/internal/llm/vocab"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session"
	fakemodel "github.com/smartpcr/agent-plugin/src/harness/internal/testkit/model"
	"github.com/smartpcr/agent-plugin/src/harness/internal/wire/sdkserver"
	"github.com/smartpcr/agent-plugin/src/harness/internal/wire/stdio"
	"github.com/smartpcr/agent-plugin/src/sdk/jsonrpc"
	"github.com/smartpcr/agent-plugin/src/sdk/protocol"
)

// The component injects llm.runtime: with the runtime provided only after the
// component is used, it serves nothing until then, and it then checks routes
// against that runtime. The driver's own runtime has route r/a; the late one has
// none, so session/create on r/a is refused CodeNoRoute. An optional lookup at
// load would have served with no route check and let the session start.
func TestComponentWaitsForTheRuntimeAndChecksRoutesAgainstIt(t *testing.T) {
	r := newRig(t)
	root := r.o.Owner
	serverIn, clientIn := io.Pipe()
	clientOut, serverOut := io.Pipe()
	t.Cleanup(func() { clientIn.Close(); serverOut.Close() })
	owner := must(stdio.Attach(stdio.Config{App: launch.NewApp()}, serverIn, serverOut))
	must(0, must(root.Apply("services", func(c *cordis.Context) error {
		if _, err := agent.Agents.Provide(c, r.reg); err != nil {
			return err
		}
		if _, err := session.Key.Provide(c, r.svc); err != nil {
			return err
		}
		_, err := stdio.Key.Provide(c, owner)
		return err
	})).Wait())
	fiber := must(root.Use(sdkserver.Component, cordis.None{}))
	if err := fiber.Wait(); err != nil || fiber.State() != cordis.Pending {
		t.Fatalf("wire.sdk without llm.runtime settled %v (err %v), want pending", fiber.State(), err)
	}

	must(0, must(root.Apply("runtime", func(c *cordis.Context) error {
		_, err := llm.RuntimeKey.Provide(c, llm.New())
		return err
	})).Wait())
	if err := fiber.Wait(); err != nil || fiber.State() != cordis.Active {
		t.Fatalf("wire.sdk with llm.runtime settled %v (err %v), want active", fiber.State(), err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	conn := jsonrpc.Serve(ctx, clientOut, clientIn, jsonrpc.Options{})
	defer conn.Close()
	var init protocol.InitializeResult
	if err := conn.Call(ctx, protocol.InitializeMethod, protocol.InitializeParams{ProtocolVersions: []int{protocol.Version}, ClientInfo: protocol.Implementation{Name: "test", Version: "0"}}, &init); err != nil {
		t.Fatalf("initialize: %v", err)
	}
	var created protocol.SessionCreateResult
	err := conn.Call(ctx, "session/create", protocol.SessionCreateParams{Model: &protocol.ModelRoute{Provider: string(route), Model: fakemodel.ModelName}}, &created)
	var rpcErr *jsonrpc.Error
	if !errors.As(err, &rpcErr) || rpcErr.Reason != string(vocab.CodeNoRoute) {
		t.Fatalf("session/create on a route the injected runtime lacks = %+v, %v; want %s", created, err, vocab.CodeNoRoute)
	}
}
