package boot

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/smartpcr/agent-plugin/src/go/loader"
	"github.com/smartpcr/agent-plugin/src/harness/internal/boot/config"
	"github.com/smartpcr/agent-plugin/src/harness/internal/boot/profile"
)

// startReload starts the validated reload of the layers (DH-D-BOOT-7) once the app
// is ready, when the profile reloads: a config.Service over the same stack, built
// from the bytes the boot read so a change saved since is seen at the first poll,
// and its watch. A rejected change is printed with its position and the running
// tree is left as it is. shutdown stops the watch before anything else.
func (s *State) startReload(ctx context.Context) {
	interval := s.Options.Profile.Reload
	if interval <= 0 || s.Service == nil {
		return
	}
	def, data := s.Options.Profile, s.Data
	opts := config.ServiceOptions{
		Stack: func(read config.ReadFile) ([]loader.Layer, []loader.Issue, error) {
			return profile.Stack(def, profile.Sources{Home: data.Home, Overlays: data.Overlays, Safe: data.Safe, Read: profile.ReadFile(read)})
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

// note prints a line to the diagnostic stream and the log ring.
func (s *State) note(format string, args ...any) {
	line := fmt.Sprintf(format, args...)
	_, _ = fmt.Fprint(s.Stderr, line)
	if s.Ring != nil {
		_, _ = fmt.Fprint(s.Ring, line)
	}
}
