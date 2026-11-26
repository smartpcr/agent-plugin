//go:build e2e

package e2e

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cucumber/godog"

	"github.com/smartpcr/agent-plugin/src/go/cordis"
	"github.com/smartpcr/agent-plugin/src/harness/internal/agent"
	"github.com/smartpcr/agent-plugin/src/harness/internal/agent/driver"
	"github.com/smartpcr/agent-plugin/src/harness/internal/llm"
	"github.com/smartpcr/agent-plugin/src/harness/internal/llm/vocab"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session/scope"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session/store/memstore"
	fakemodel "github.com/smartpcr/agent-plugin/src/harness/internal/testkit/model"
	"github.com/smartpcr/agent-plugin/src/harness/internal/tools"
	"github.com/smartpcr/agent-plugin/src/harness/internal/tools/core"
	"github.com/smartpcr/agent-plugin/src/harness/internal/tools/prompt"
	"github.com/smartpcr/agent-plugin/src/harness/internal/wire/sdkserver"
	"github.com/smartpcr/agent-plugin/src/sdk/jsonrpc"
	"github.com/smartpcr/agent-plugin/src/sdk/jsonrpc/jsonrpctest"
	"github.com/smartpcr/agent-plugin/src/sdk/protocol"
)

const ssmRoute vocab.RouteID = "r/ssm"

// ssmT is the suite's *testing.T: the fake model, the peer and the cleanups of
// the server need a testing.TB.
var ssmT *testing.T

// ssmPrompt assembles a fixed system prompt and the view's tools.
type ssmPrompt struct{}

func (ssmPrompt) Assemble(_ context.Context, in prompt.Input) (prompt.Assembly, error) {
	a := prompt.Assembly{Sections: []prompt.RenderedSection{{Name: "e2e", Order: prompt.OrderHarness, Owner: "sdk-session-methods-e2e", Text: "You are the e2e agent."}}}
	for _, d := range in.View.Tools() {
		a.Tools = append(a.Tools, d.Schema())
	}
	fp, err := vocab.Digest(a.Tools)
	a.Fingerprint = fp
	return a, err
}

func (ssmPrompt) Render(a prompt.Assembly) (string, error) { return a.Sections[0].Text, nil }

type ssmState struct {
	peer      *jsonrpctest.Peer
	next      int64
	sessionID string
	waitSeq   int64
	history   protocol.SessionHistoryResult

	committed   []byte
	regenerated []byte
}

func ssmWait(f *cordis.Fiber, err error) error {
	if err != nil {
		return err
	}
	return f.Wait()
}

// newServer builds a driven agent registry over a memstore and the fake model,
// and the SDK server over them, as cmd/dsh wires it.
func (s *ssmState) newServer(reply string) (*sdkserver.Server, error) {
	t := ssmT
	root := cordis.New()
	root.Loggers().ClearExporters()
	root.Loggers().AddExporter(&cordis.MemoryExporter{})
	t.Cleanup(root.Dispose)
	for _, c := range []*cordis.Component{scope.Component, core.Component} {
		if err := ssmWait(root.Use(c, cordis.None{})); err != nil {
			return nil, fmt.Errorf("use %v: %w", c, err)
		}
	}
	var tsvc tools.Service
	if err := ssmWait(root.Inject([]string{tools.Key.Name()}, func(c *cordis.Context) (err error) {
		tsvc, err = tools.Key.Get(c)
		return err
	})); err != nil {
		return nil, err
	}
	svc := session.NewService(root, memstore.New(), session.Config{})
	reg := agent.NewRegistry(root)
	rt := llm.New()
	fakemodel.Register(t, rt, ssmRoute, fakemodel.NewTransport(t, fakemodel.Reply(reply)))
	var drvCtx *cordis.Context
	if err := ssmWait(root.Apply("driver", func(c *cordis.Context) error { drvCtx = c; return nil })); err != nil {
		return nil, err
	}
	f, err := driver.NewFactory(driver.FactoryOptions{
		Context: drvCtx, Sessions: svc, Scopes: scope.NewTable(root), Models: rt, Tools: tsvc, Prompt: ssmPrompt{},
	})
	if err != nil {
		return nil, err
	}
	if _, err := reg.SetFactory(root, f); err != nil {
		return nil, err
	}
	return sdkserver.New(sdkserver.Options{
		Owner: root, Agents: reg, Sessions: svc, Routes: rt,
		Info: protocol.Implementation{Name: "dsh", Version: "e2e"}, Limits: protocol.Limits{MaxFrameBytes: 1 << 20},
	})
}

func (s *ssmState) call(method string, params, result any) error {
	s.next++
	id := jsonrpc.Int64ID(s.next)
	s.peer.Request(id, method, params)
	r := s.peer.ExpectResponse(id)
	if r.Error != nil {
		return fmt.Errorf("%s: %v", method, r.Error)
	}
	if err := json.Unmarshal(r.Result, result); err != nil {
		return fmt.Errorf("%s: decode %s: %w", method, r.Result, err)
	}
	return nil
}

func (s *ssmState) givenPeer(reply string) error {
	srv, err := s.newServer(reply)
	if err != nil {
		return err
	}
	c, err := srv.Connect()
	if err != nil {
		return err
	}
	ssmT.Cleanup(func() { _ = c.Close(context.Background()) })
	_, s.peer = jsonrpctest.NewPeer(ssmT, jsonrpc.Options{Methods: c.Methods()})
	var init protocol.InitializeResult
	if err := s.call("initialize", protocol.InitializeParams{ProtocolVersions: []int{protocol.Version}, ClientInfo: protocol.Implementation{Name: "e2e", Version: "0"}}, &init); err != nil {
		return err
	}
	if init.ProtocolVersion != protocol.Version || init.ServerInfo.Name != "dsh" {
		return fmt.Errorf("initialize = %+v", init)
	}
	return nil
}

func (s *ssmState) createSession() error {
	var r protocol.SessionCreateResult
	if err := s.call("session/create", protocol.SessionCreateParams{Model: &protocol.ModelRoute{Provider: string(ssmRoute), Model: fakemodel.ModelName}}, &r); err != nil {
		return err
	}
	if r.SessionID == "" {
		return fmt.Errorf("session/create = %+v, want a session id", r)
	}
	s.sessionID = r.SessionID
	return nil
}

func (s *ssmState) promptSession(text, messageID string) error {
	var r protocol.SessionPromptResult
	if err := s.call("session/prompt", protocol.SessionPromptParams{SessionID: s.sessionID, Content: []protocol.ContentBlock{{Type: "text", Text: text}}, MessageID: messageID}, &r); err != nil {
		return err
	}
	if r.MessageID != messageID {
		return fmt.Errorf("prompt receipt = %+v, want message id %q", r, messageID)
	}
	return nil
}

func (s *ssmState) waitSession() error {
	var w protocol.LastSeqResult
	if err := s.call("session/wait", protocol.SessionRef{SessionID: s.sessionID}, &w); err != nil {
		return err
	}
	s.waitSeq = w.LastSeq
	return nil
}

func (s *ssmState) callHistory(a, b string) error {
	return s.call("session/history", protocol.SessionHistoryParams{SessionID: s.sessionID, Types: []string{a, b}}, &s.history)
}

func (s *ssmState) historyHolds(n int) error {
	if len(s.history.Events) != n || s.history.More {
		return fmt.Errorf("history = %d events (more %v), want %d and no more: %+v", len(s.history.Events), s.history.More, n, s.history)
	}
	return nil
}

func (s *ssmState) historyEvent(i int, typ, text string) error {
	if i < 1 || i > len(s.history.Events) {
		return fmt.Errorf("history has %d events, no event %d", len(s.history.Events), i)
	}
	e := s.history.Events[i-1]
	if e.Type != typ || !strings.Contains(string(e.Data), text) {
		return fmt.Errorf("history event %d = %s %s, want a %s holding %q", i, e.Type, e.Data, typ, text)
	}
	return nil
}

func (s *ssmState) userBeforeAssistant() error {
	if len(s.history.Events) != 2 {
		return fmt.Errorf("history = %+v, want two events", s.history)
	}
	if u, a := s.history.Events[0].Seq, s.history.Events[1].Seq; u >= a {
		return fmt.Errorf("user message seq %d, assistant message seq %d: want the user first", u, a)
	}
	return nil
}

func (s *ssmState) lastSeqMatchesWait() error {
	if s.history.LastSeq != s.waitSeq {
		return fmt.Errorf("history lastSeq = %d, session/wait returned %d", s.history.LastSeq, s.waitSeq)
	}
	return nil
}

// ssmSDKDir finds src/sdk by walking up from the working directory.
func ssmSDKDir() (string, error) {
	dir, err := os.Getwd()
	if err != nil {
		return "", err
	}
	for {
		cand := filepath.Join(dir, "src", "sdk")
		if _, err := os.Stat(filepath.Join(cand, "protocol", "internal", "genschema", "main.go")); err == nil {
			return cand, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", errors.New("src/sdk not found above the working directory")
		}
		dir = parent
	}
}

func (s *ssmState) givenCommittedSchema() error {
	sdk, err := ssmSDKDir()
	if err != nil {
		return err
	}
	s.committed, err = os.ReadFile(filepath.Join(sdk, "protocol", "schema.json"))
	if err != nil {
		return err
	}
	if len(s.committed) == 0 {
		return errors.New("the committed schema.json is empty")
	}
	return nil
}

// regenerate runs internal/genschema the way go generate does, into a temp file.
func (s *ssmState) regenerate() error {
	sdk, err := ssmSDKDir()
	if err != nil {
		return err
	}
	out := filepath.Join(ssmT.TempDir(), "schema.json")
	cmd := exec.Command("go", "run", "./protocol/internal/genschema", out)
	cmd.Dir = sdk
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("go run ./protocol/internal/genschema: %w: %s", err, stderr.String())
	}
	s.regenerated, err = os.ReadFile(out)
	return err
}

func (s *ssmState) schemaIdentical() error {
	if !bytes.Equal(s.regenerated, s.committed) {
		return fmt.Errorf("regenerated schema.json (%d bytes) differs from the committed file (%d bytes): run go -C src/sdk generate ./protocol", len(s.regenerated), len(s.committed))
	}
	return nil
}

func InitializeScenario_dh_s_15_listener_sign_in_stdio_and_sdk_server_sdk_session_methods(ctx *godog.ScenarioContext) {
	var s *ssmState
	ctx.Before(func(c context.Context, _ *godog.Scenario) (context.Context, error) {
		s = &ssmState{}
		return c, nil
	})
	ctx.Step(`^a jsonrpctest peer initialized against an SDK server over a memstore and a fake model replying "([^"]*)"$`, func(r string) error { return s.givenPeer(r) })
	ctx.Step(`^the client creates a session$`, func() error { return s.createSession() })
	ctx.Step(`^the client prompts "([^"]*)" with message id "([^"]*)"$`, func(t, id string) error { return s.promptSession(t, id) })
	ctx.Step(`^the client waits for the session to settle$`, func() error { return s.waitSession() })
	ctx.Step(`^the client calls session/history for "([^"]*)" and "([^"]*)"$`, func(a, b string) error { return s.callHistory(a, b) })
	ctx.Step(`^history holds (\d+) events$`, func(n int) error { return s.historyHolds(n) })
	ctx.Step(`^history event (\d+) is an? "([^"]*)" holding "([^"]*)"$`, func(i int, typ, text string) error { return s.historyEvent(i, typ, text) })
	ctx.Step(`^the user message comes before the assistant message$`, func() error { return s.userBeforeAssistant() })
	ctx.Step(`^history's lastSeq is the lastSeq session/wait returned$`, func() error { return s.lastSeqMatchesWait() })
	ctx.Step(`^the committed wire schema src/sdk/protocol/schema\.json$`, func() error { return s.givenCommittedSchema() })
	ctx.Step(`^internal/genschema regenerates schema\.json into a temp directory$`, func() error { return s.regenerate() })
	ctx.Step(`^the regenerated schema is byte-identical to the committed file$`, func() error { return s.schemaIdentical() })
}

func TestE2E_dh_s_15_listener_sign_in_stdio_and_sdk_server_sdk_session_methods(t *testing.T) {
	ssmT = t
	suite := godog.TestSuite{
		ScenarioInitializer: InitializeScenario_dh_s_15_listener_sign_in_stdio_and_sdk_server_sdk_session_methods,
		Options: &godog.Options{
			Format:   "pretty",
			Paths:    []string{"dh_s_15_listener_sign_in_stdio_and_sdk_server_sdk_session_methods.feature"},
			TestingT: t,
			Strict:   true,
		},
	}
	if suite.Run() != 0 {
		t.Fatal("the sdk session methods e2e scenarios failed")
	}
}
