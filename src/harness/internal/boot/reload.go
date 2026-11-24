package boot

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/smartpcr/agent-plugin/src/go/loader"
	"github.com/smartpcr/agent-plugin/src/harness/internal/boot/agentpreset"
	"github.com/smartpcr/agent-plugin/src/harness/internal/boot/config"
	"github.com/smartpcr/agent-plugin/src/harness/internal/boot/profile"
	"github.com/smartpcr/agent-plugin/src/harness/internal/util/herr"
)

// startReload starts the validated reload of the layers (DH-D-BOOT-7) once the app
// is ready, when the profile reloads: a config.Service over the same stack, built
// from the bytes the boot read so a change saved since is seen at the first poll,
// and its watch. A rejected change is printed with its position and the running
// tree is left as it is. The agent presets are checked as one set
// (agentpreset.CheckTree), so an edit that breaks their inheritance or default is
// rejected whole. shutdown stops the watch before anything else.
func (s *State) startReload(ctx context.Context) {
	interval := s.Options.Profile.Reload
	if interval <= 0 || s.Service == nil {
		return
	}
	def, data := s.Options.Profile, s.Data
	opts := config.ServiceOptions{
		Stack: func(read config.ReadFile) ([]loader.Layer, []loader.Issue, error) {
			return profile.Stack(def, profile.Sources{Home: data.Home, Overlays: data.Overlays, Read: profile.ReadFile(read)})
		},
		Loader:    s.Service,
		Context:   s.root,
		Evaluator: s.evaluator,
		Required:  def.Required,
		Read:      layerReader(s.Options.Request.ReadFile),
		Baseline:  s.layerFiles,
	}
	if s.catalog != nil {
		opts.Catalog = s.catalog
	}
	opts.TreeChecks = s.treeChecks()
	svc, err := config.NewService(opts)
	if err != nil {
		s.note("dsh: reload off: %v\n", err)
		return
	}
	svc.OnRejected(func(err error) { s.note("dsh: %v\n", err) })
	s.Config = svc

	wctx, cancel := context.WithCancel(ctx)
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		svc.Watch(wctx, interval)
	}()
	s.stopReload = func(budget time.Duration) {
		s.stopReload = nil
		svc.Close()
		cancel()
		done := make(chan struct{})
		go func() {
			wg.Wait()
			close(done)
		}()
		select {
		case <-done:
		case <-s.Clock.After(forceReserve(budget)):
			s.note("dsh: a reload still running at shutdown was left to finish\n")
		}
	}
}

// treeChecks are the whole-tree rules the start-up tree and every reload are
// held to: the agent presets as one set (agentpreset.CheckTree).
func (s *State) treeChecks() []config.TreeCheck {
	if s.catalog == nil {
		return []config.TreeCheck{agentpreset.CheckTree(nil)}
	}
	return []config.TreeCheck{agentpreset.CheckTree(s.catalog)}
}

// checkTree runs the whole-tree rules over the composed tree before the first
// apply, so a start-up tree is held to the rule a reload is: an agent preset set
// whose Default or Extends names no declaration, or whose inheritance closes a
// cycle, stops the launch (config/rejected, status 78) with every issue placed.
func (s *State) checkTree() error {
	var issues []loader.Issue
	for _, check := range s.treeChecks() {
		issues = append(issues, check(s.Composed)...)
	}
	if len(issues) == 0 {
		return nil
	}
	for _, issue := range issues {
		if s.Ring != nil {
			_, _ = fmt.Fprintf(s.Ring, "boot: tree issue: %s\n", issue.Error())
		}
	}
	return herr.Wrap(config.ReasonRejected, &loader.ParseError{Issues: issues}, "start-up tree rejected")
}

// note prints a line to the diagnostic stream and the log ring.
func (s *State) note(format string, args ...any) {
	line := fmt.Sprintf(format, args...)
	_, _ = fmt.Fprint(s.Stderr, line)
	if s.Ring != nil {
		_, _ = fmt.Fprint(s.Ring, line)
	}
}
