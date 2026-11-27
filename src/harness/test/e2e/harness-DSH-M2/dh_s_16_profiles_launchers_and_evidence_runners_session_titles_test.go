//go:build e2e

package e2e

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/cucumber/godog"

	"github.com/smartpcr/agent-plugin/src/go/cordis"
	"github.com/smartpcr/agent-plugin/src/harness/internal/llm"
	"github.com/smartpcr/agent-plugin/src/harness/internal/llm/replay"
	"github.com/smartpcr/agent-plugin/src/harness/internal/llm/titlemodel"
	"github.com/smartpcr/agent-plugin/src/harness/internal/llm/vocab"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session/event"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session/scope"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session/store/memstore"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session/title"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session/views"
	"github.com/smartpcr/agent-plugin/src/harness/internal/util/herr"
)

var sttlBg = context.Background()

// sttlState is one scenario: the llm runtime with the replay route, a memstore
// session service, the title service bound through llm/titlemodel, and the
// session under test.
type sttlState struct {
	tmp    string
	reqs   string
	root   *cordis.Context
	svc    session.Service
	titles *title.Service
	ended  chan struct{}
	h      session.Handle

	pinned  title.Title
	lastErr error
}

func (s *sttlState) cleanup() {
	if s.h != nil {
		_ = s.h.Close(sttlBg)
	}
	if s.titles != nil {
		s.titles.Wait()
	}
	if s.root != nil {
		s.root.Dispose()
	}
	if s.tmp != "" {
		_ = os.RemoveAll(s.tmp)
	}
}

// service writes a replay scenario whose one recorded reply is reply, loads
// llm.replay on route "replay" and starts the title service on it.
func (s *sttlState) service(reply string) error {
	tmp, err := os.MkdirTemp("", "dsh-titles-")
	if err != nil {
		return err
	}
	s.tmp = tmp
	scenario, reqs := filepath.Join(tmp, "scenario"), filepath.Join(tmp, "requests")
	for _, d := range []string{scenario, reqs} {
		if err := os.MkdirAll(d, 0o700); err != nil {
			return err
		}
	}
	m, err := vocab.RestoreMessage("t1", vocab.RoleAssistant, nil, vocab.TextBlock(reply))
	if err != nil {
		return err
	}
	data, err := json.Marshal(map[string]any{"message": m, "request_digest": "<digest>", "usage": vocab.Usage{Input: 9, Output: 3}})
	if err != nil {
		return err
	}
	line, err := json.Marshal(map[string]any{"type": "assistant/message", "data": string(data)})
	if err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(scenario, replay.SessionFile), []byte(`{"dsh_export":1}`+"\n"+string(line)+"\n"), 0o600); err != nil {
		return err
	}

	root := cordis.New()
	s.root = root
	rt := llm.New(llm.WithInterception(root))
	f, err := root.Apply("runtime", func(c *cordis.Context) error {
		_, err := llm.RuntimeKey.Provide(c, rt)
		return err
	})
	if err != nil {
		return err
	}
	if err := f.Wait(); err != nil {
		return err
	}
	fiber, err := root.Use(replay.Component, replay.Config{Scenario: scenario, Route: "replay", Requests: reqs})
	if err != nil {
		return err
	}
	if err := fiber.Wait(); err != nil {
		return fmt.Errorf("load llm.replay: %w", err)
	}

	reg := views.New()
	ts, err := title.New(reg, title.WithModel(titlemodel.New(rt)))
	if err != nil {
		return err
	}
	if err := ts.Listen(root); err != nil {
		return err
	}
	// Registered after the title service's listener, so a turn/end seen here
	// has already started any generation it was due.
	s.ended = make(chan struct{}, 16)
	if _, err := session.Committed.On(root, func(_ *cordis.Context, a session.CommittedArgs) error {
		if a.Event.Type() == event.TurnEndType.Name() {
			s.ended <- struct{}{}
		}
		return nil
	}); err != nil {
		return err
	}
	s.reqs, s.titles = reqs, ts
	s.svc = session.NewService(root, memstore.New(), session.Config{Folder: reg})
	return nil
}

// session creates a session (attached to Titles by session.Created) and opens
// turn 1 with the user's prompt on route replay.
func (s *sttlState) session(text string) error {
	h, err := s.svc.Create(sttlBg, nil, session.CreateOptions{Header: session.Header{}, Scope: scope.Key{}})
	if err != nil {
		return err
	}
	s.h = h
	user, err := vocab.RestoreMessage("u1", vocab.RoleUser, nil, vocab.TextBlock(text))
	if err != nil {
		return err
	}
	var drafts []event.Draft
	for _, mk := range []func() (event.Draft, error){
		func() (event.Draft, error) { return event.TurnStartType.Draft(event.TurnStart{Turn: 1}) },
		func() (event.Draft, error) { return event.StepStartType.Draft(event.StepStart{Turn: 1, Step: 1}) },
		func() (event.Draft, error) {
			return event.RequestHeader.Draft(event.RequestHeaderPayload{Route: "replay"})
		},
		func() (event.Draft, error) {
			return event.UserMessage.Draft(event.Append(), event.UserMessagePayload{Message: user})
		},
	} {
		d, err := mk()
		if err != nil {
			return err
		}
		drafts = append(drafts, d)
	}
	if _, err := h.AppendGroup(sttlBg, drafts); err != nil {
		return err
	}
	_, err = h.Flush(sttlBg)
	return err
}

func (s *sttlState) get() (title.Title, error) { return s.titles.Get(sttlBg, s.h.ID()) }

func (s *sttlState) titleLog() ([]event.Event, error) {
	evs, err := s.h.Read(sttlBg, 0, int(s.h.Len()))
	if err != nil {
		return nil, err
	}
	var out []event.Event
	for _, ev := range evs {
		if strings.HasPrefix(ev.Type(), "session/title") {
			out = append(out, ev)
		}
	}
	return out, nil
}

func (s *sttlState) firstTurnCompletes() error {
	sd, err := event.StepEndType.Draft(event.StepEnd{Turn: 1, Step: 1})
	if err != nil {
		return err
	}
	td, err := event.TurnEndType.Draft(event.TurnEnd{Turn: 1, Reason: event.Reason{Kind: event.ReasonCompleted}})
	if err != nil {
		return err
	}
	if _, err := s.h.AppendGroup(sttlBg, []event.Draft{sd, td}); err != nil {
		return err
	}
	if _, err := s.h.Flush(sttlBg); err != nil {
		return err
	}
	select {
	case <-s.ended:
	case <-time.After(10 * time.Second):
		return errors.New("turn/end was never delivered")
	}
	s.titles.Wait()
	return nil
}

func (s *sttlState) showsFallback(want string) error {
	got, err := s.get()
	if err != nil {
		return err
	}
	if got.Text != want || got.Source != title.SourceFallback || got.Pinned {
		return fmt.Errorf("fallback = %+v, want text %q from %q, unpinned", got, want, title.SourceFallback)
	}
	return nil
}

func (s *sttlState) noTitleEvents() error {
	evs, err := s.titleLog()
	if err != nil {
		return err
	}
	if len(evs) != 0 {
		return fmt.Errorf("the log holds %d title events before any generation", len(evs))
	}
	return nil
}

func (s *sttlState) showsGenerated(want string) error {
	got, err := s.get()
	if err != nil {
		return err
	}
	if got.Text != want || got.Source != title.SourceGenerated || got.Pinned || got.RequestSeq == nil || len(got.Pending) != 0 {
		return fmt.Errorf("generated = %+v, want text %q from %q, unpinned, settled", got, want, title.SourceGenerated)
	}
	return nil
}

func (s *sttlState) eventsInOrder(want string) error {
	evs, err := s.titleLog()
	if err != nil {
		return err
	}
	types := make([]string, len(evs))
	for i, ev := range evs {
		types[i] = ev.Type()
	}
	if got := strings.Join(types, ","); got != want {
		return fmt.Errorf("title events = %s, want %s", got, want)
	}
	return nil
}

func (s *sttlState) attemptAccepted() error {
	evs, err := s.titleLog()
	if err != nil {
		return err
	}
	if len(evs) != 3 {
		return fmt.Errorf("want 3 title events, got %d", len(evs))
	}
	a, err := title.AttemptType.As(evs[1])
	if err != nil {
		return err
	}
	if a.Outcome != title.OutcomeAccepted || a.RequestSeq != evs[0].Seq() || a.ServedBy != replay.ServedBy || a.Usage == nil {
		return fmt.Errorf("attempt = %+v, want accepted for request %d served by %q with usage", a, evs[0].Seq(), replay.ServedBy)
	}
	return nil
}

func (s *sttlState) titleNamesRequest() error {
	evs, err := s.titleLog()
	if err != nil {
		return err
	}
	if len(evs) != 3 {
		return fmt.Errorf("want 3 title events, got %d", len(evs))
	}
	p, err := title.TitleType.As(evs[2])
	if err != nil {
		return err
	}
	if p.RequestSeq == nil || *p.RequestSeq != evs[0].Seq() || len(p.MessageSeqs) != 1 || p.Source != title.SourceGenerated {
		return fmt.Errorf("title event = %+v, want generated for request %d over one prompt", p, evs[0].Seq())
	}
	return nil
}

func (s *sttlState) sentIsRebuilt() error {
	evs, err := s.titleLog()
	if err != nil {
		return err
	}
	if len(evs) == 0 {
		return errors.New("no title request was logged")
	}
	sent, err := os.ReadFile(filepath.Join(s.reqs, "000.json"))
	if err != nil {
		return fmt.Errorf("the replay route recorded no request: %w", err)
	}
	req, err := s.h.RequestFrom(evs[0].Seq())
	if err != nil {
		return err
	}
	if want := req.Canonical() + "\n"; string(sent) != want {
		return fmt.Errorf("sent %s, rebuilt %s", sent, want)
	}
	return nil
}

func (s *sttlState) rename(text string) error {
	t, err := s.titles.Rename(sttlBg, s.h.ID(), text)
	if err != nil {
		return err
	}
	s.pinned = t
	return nil
}

func (s *sttlState) showsPinned(want string) error {
	got, err := s.get()
	if err != nil {
		return err
	}
	if got.Text != want || got.Source != title.SourceUser || !got.Pinned {
		return fmt.Errorf("title = %+v, want pinned %q from %q", got, want, title.SourceUser)
	}
	return nil
}

func (s *sttlState) refresh() error {
	_, s.lastErr = s.titles.Refresh(sttlBg, s.h.ID())
	return nil
}

func (s *sttlState) refusedPinned() error {
	if r, _ := herr.ReasonOf(s.lastErr); r != title.CodePinned {
		return fmt.Errorf("Refresh of a pinned title returned %v, want %s", s.lastErr, title.CodePinned)
	}
	return nil
}

func (s *sttlState) stillPinned(want string) error {
	if err := s.showsPinned(want); err != nil {
		return err
	}
	got, err := s.get()
	if err != nil {
		return err
	}
	if got.Revision != s.pinned.Revision {
		return fmt.Errorf("revision moved from %d to %d", s.pinned.Revision, got.Revision)
	}
	return nil
}

func (s *sttlState) noRequestLogged() error {
	evs, err := s.titleLog()
	if err != nil {
		return err
	}
	for _, ev := range evs {
		if ev.Type() == title.RequestType.Name() {
			return fmt.Errorf("a title request was logged at seq %d", ev.Seq())
		}
	}
	return nil
}

func (s *sttlState) unpin() error {
	_, err := s.titles.Unpin(sttlBg, s.h.ID())
	return err
}

func (s *sttlState) showsUnpinned(want string) error {
	got, err := s.get()
	if err != nil {
		return err
	}
	if got.Text != want || got.Source != title.SourceUser || got.Pinned {
		return fmt.Errorf("title = %+v, want unpinned %q from %q", got, want, title.SourceUser)
	}
	return nil
}

func (s *sttlState) refreshThenGenerated(want string) error {
	if s.lastErr != nil {
		return fmt.Errorf("Refresh after Unpin: %w", s.lastErr)
	}
	return s.showsGenerated(want)
}

func InitializeScenario_dh_s_16_profiles_launchers_and_evidence_runners_session_titles(ctx *godog.ScenarioContext) {
	s := &sttlState{}
	ctx.After(func(c context.Context, _ *godog.Scenario, err error) (context.Context, error) {
		s.cleanup()
		*s = sttlState{}
		return c, err
	})
	ctx.Step(`^a title service whose replay route records the title reply "([^"]*)"$`, s.service)
	ctx.Step(`^a session whose first turn opens with the prompt "([^"]*)"$`, s.session)
	ctx.Step(`^the title fold shows the fallback "([^"]*)"$`, s.showsFallback)
	ctx.Step(`^the session log holds no title events$`, s.noTitleEvents)
	ctx.Step(`^the first turn completes$`, s.firstTurnCompletes)
	ctx.Step(`^the title fold shows the generated title "([^"]*)"$`, s.refreshThenGenerated)
	ctx.Step(`^the session log holds the title events "([^"]*)" in order$`, s.eventsInOrder)
	ctx.Step(`^the attempt settled the request as accepted, served by the replay route$`, s.attemptAccepted)
	ctx.Step(`^the title event names the request and the prompt it covered$`, s.titleNamesRequest)
	ctx.Step(`^the request sent to the model is the one rebuilt from the logged request event$`, s.sentIsRebuilt)
	ctx.Step(`^the session is renamed to "([^"]*)"$`, s.rename)
	ctx.Step(`^the title fold shows the pinned user title "([^"]*)"$`, s.showsPinned)
	ctx.Step(`^Refresh runs on the session$`, s.refresh)
	ctx.Step(`^Refresh is refused as title/pinned$`, s.refusedPinned)
	ctx.Step(`^the title fold still shows the pinned user title "([^"]*)" at the same revision$`, s.stillPinned)
	ctx.Step(`^no title request was logged$`, s.noRequestLogged)
	ctx.Step(`^the title is unpinned$`, s.unpin)
	ctx.Step(`^the title fold shows the unpinned user title "([^"]*)"$`, s.showsUnpinned)
}

func TestE2E_dh_s_16_profiles_launchers_and_evidence_runners_session_titles(t *testing.T) {
	suite := godog.TestSuite{
		ScenarioInitializer: InitializeScenario_dh_s_16_profiles_launchers_and_evidence_runners_session_titles,
		Options: &godog.Options{
			Format:   "pretty",
			Paths:    []string{"dh_s_16_profiles_launchers_and_evidence_runners_session_titles.feature"},
			TestingT: t,
		},
	}
	if suite.Run() != 0 {
		t.Fatal("godog scenarios failed")
	}
}
