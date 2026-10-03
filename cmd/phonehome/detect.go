package main

import (
	"context"
	"log/slog"
	"slices"
	"sync"
	"time"

	"github.com/bizpers11991-code/phonehome/internal/config"
	"github.com/bizpers11991-code/phonehome/internal/ingest"
	"github.com/bizpers11991-code/phonehome/internal/model"
	"github.com/bizpers11991-code/phonehome/internal/store"
)

// redetectEvery is how often `serve` looks for well-known files again while
// auto-detection has not found a DNS source: on a fresh install Pi-hole or
// AdGuard Home may not have created them yet, or the user is still fixing
// permissions.
const redetectEvery = time.Minute

// setupState is what the dashboard and CLI are told about source selection.
// serve updates it from the re-detection loop, so it is locked.
type setupState struct {
	mu sync.Mutex
	v  model.Setup
}

func (s *setupState) get() model.Setup {
	if s == nil {
		return model.Setup{}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.v // set always stores fresh slices, so sharing them is safe
}

func (s *setupState) set(v model.Setup) {
	s.mu.Lock()
	s.v = v
	s.mu.Unlock()
}

// newSetup describes the sources in use and, in auto-detect mode (det not
// nil), what detection could not read.
func newSetup(srcs []config.Source, det *config.Detection, at time.Time) model.Setup {
	out := model.Setup{}
	for _, s := range srcs {
		loc := s.Path
		if loc == "" {
			loc = s.URL
		}
		out.Sources = append(out.Sources, model.SetupSource{Type: s.Type, Location: loc})
	}
	if det == nil {
		return out
	}
	out.AutoDetect, out.CheckedAt = true, at
	for _, p := range det.Problems {
		out.Problems = append(out.Problems, model.SetupProblem{
			Type: p.Type, Path: p.Path, Problem: p.Err, Hint: p.Hint, Optional: p.Optional,
		})
	}
	return out
}

// logDetection logs what changed between two detections: sources found or
// lost, and problems that are new. Unchanged findings are not repeated, so
// re-detecting every minute does not flood the log.
func logDetection(log *slog.Logger, prev, next config.Detection) {
	for _, s := range next.Sources {
		if !slices.Contains(prev.Sources, s) {
			log.Info("auto-detected source", "type", s.Type, "path", s.Path)
		}
	}
	for _, s := range prev.Sources {
		if !slices.Contains(next.Sources, s) {
			log.Warn("auto-detected source is gone", "type", s.Type, "path", s.Path)
		}
	}
	for _, p := range next.Problems {
		if slices.Contains(prev.Problems, p) {
			continue
		}
		if p.Optional {
			log.Info("optional source not readable", "type", p.Type, "path", p.Path, "err", p.Err, "hint", p.Hint)
		} else {
			log.Warn("source found but not readable", "type", p.Type, "path", p.Path, "err", p.Err, "hint", p.Hint)
		}
	}
}

// redetect runs detect every interval until it finds a DNS source. Each
// result goes to apply together with the one before; apply returns false
// when it could not use the new sources, so the next round tries again.
// Once a DNS source is in use, the set is kept: a file that disappears for
// a moment (log rotation, a Pi-hole restart) must not stop ingestion.
func redetect(ctx context.Context, every time.Duration, cur config.Detection,
	detect func() config.Detection, apply func(prev, next config.Detection) bool) {
	t := time.NewTicker(every)
	defer t.Stop()
	for !cur.HasDNS() {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
		if next := detect(); apply(cur, next) {
			cur = next
		}
	}
}

// sameSources reports whether two detections would read the same sources.
func sameSources(a, b config.Detection) bool { return slices.Equal(a.Sources, b.Sources) }

// ingester runs ingestion over a set of sources that `serve` may replace
// when re-detection finds new files.
type ingester struct {
	cfg  *config.Config
	st   *store.Store
	app  *app
	log  *slog.Logger
	stop func() // stops the current runner and closes its sources
}

// start replaces the running sources with list. When the new sources cannot
// be built, the old ones keep running and the error is returned.
func (g *ingester) start(ctx context.Context, list []config.Source) error {
	srcs, err := buildSources(list)
	if err != nil {
		return err
	}
	opts, err := analyzeOptions(g.cfg, srcs.local)
	if err != nil {
		srcs.Close()
		return err
	}
	g.halt()
	g.app.setOptions(opts)
	runner := &ingest.Runner{
		Store: g.st, DNS: srcs.dns, Flows: srcs.flows, Devices: srcs.devices,
		Retention: g.cfg.Retention(), Logger: g.log,
	}
	rctx, cancel := context.WithCancel(ctx)
	done := make(chan struct{})
	go func() {
		defer close(done)
		runner.Run(rctx, g.cfg.Interval)
	}()
	g.stop = func() {
		cancel()
		<-done
		srcs.Close()
	}
	return nil
}

// halt stops the current runner, if any, and waits for it.
func (g *ingester) halt() {
	if g.stop != nil {
		g.stop()
		g.stop = nil
	}
}

// watchSources keeps auto-detected sources current for `serve`: it updates
// what the dashboard shows after every detection and restarts ingestion
// when the usable sources change.
func (g *ingester) watchSources(ctx context.Context, every time.Duration, cur config.Detection,
	detect func() config.Detection, setup *setupState) {
	redetect(ctx, every, cur, detect, func(prev, next config.Detection) bool {
		logDetection(g.log, prev, next)
		if !sameSources(prev, next) {
			if err := g.start(ctx, next.Sources); err != nil {
				g.log.Warn("cannot use auto-detected sources; trying again", "err", err, "retry_in", every)
				setup.set(newSetup(prev.Sources, &next, time.Now()))
				return false
			}
		}
		setup.set(newSetup(next.Sources, &next, time.Now()))
		if !next.HasDNS() {
			g.log.Debug("no DNS source yet; looking again", "in", every)
		}
		return true
	})
}
