// Package ingest moves observations from sources into the store.
//
// Delivery is at-least-once: a source's cursor is saved only after its batch
// has been written, so a crash (or a failed SetCursor) between the two makes
// the next run re-read that batch. The store is expected to tolerate the
// resulting duplicates.
package ingest

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/bizpers11991-code/phonehome/internal/model"
	"github.com/bizpers11991-code/phonehome/internal/source"
)

// Store is the subset of the store that ingestion writes to.
type Store interface {
	InsertDNS(ctx context.Context, qs []model.DNSQuery) error
	InsertFlows(ctx context.Context, fs []model.Flow) error
	UpsertDevices(ctx context.Context, ds []model.Device) error
	Cursor(ctx context.Context, source string) (string, error)
	SetCursor(ctx context.Context, source, cursor string) error
	RecordRun(ctx context.Context, st model.SourceStatus) error
	Prune(ctx context.Context, before time.Time) (int64, error)
}

const (
	// DefaultBatch is the fetch size used when Runner.Batch is zero.
	DefaultBatch = 5000
	// maxBatchesPerRun caps one source's work per run so a huge backlog
	// cannot starve the others; the rest is picked up next run.
	maxBatchesPerRun = 20
	maxBackoff       = 15 * time.Minute
	pruneEvery       = 24 * time.Hour
)

// Runner ingests from a fixed set of sources.
type Runner struct {
	Store     Store
	DNS       []source.DNSSource
	Flows     []source.FlowSource
	Devices   []source.DeviceSource
	Batch     int           // records per fetch; 0 = DefaultBatch
	Retention time.Duration // Run prunes older data daily; 0 = keep forever
	Logger    *slog.Logger  // nil = slog.Default()
	Now       func() time.Time
}

// task is one source's unit of work in a run.
type task struct {
	key  string // status name and cursor key
	kind string // "dns", "flow", "devices"
	run  func(ctx context.Context, key string) (int64, error)
}

// RunOnce ingests from every source once, then prunes data older than
// Retention (when set), so that `ingest --once` from cron keeps the store
// bounded too. Sources are independent: a failing source is recorded and
// skipped, and the errors of all failing sources are returned joined.
func (r *Runner) RunOnce(ctx context.Context) error {
	var errs []error
	for _, t := range r.tasks() {
		if err := ctx.Err(); err != nil {
			return errors.Join(append(errs, err)...)
		}
		if _, err := r.runTask(ctx, t); err != nil {
			errs = append(errs, err)
		}
	}
	if r.Retention > 0 {
		if err := r.prune(ctx); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

// Run ingests every interval until ctx is cancelled. A source that keeps
// failing is retried with exponential backoff (capped at 15 minutes), and
// old data is pruned once a day when Retention is set.
func (r *Runner) Run(ctx context.Context, interval time.Duration) {
	tick := time.NewTicker(interval)
	defer tick.Stop()
	backoffs := map[string]*backoff{}
	var lastPrune time.Time
	for {
		r.cycle(ctx, interval, backoffs)
		if r.Retention > 0 && r.now().Sub(lastPrune) >= pruneEvery {
			lastPrune = r.now()
			if err := r.prune(ctx); err != nil {
				r.log().Warn("prune failed", "err", err)
			}
		}
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
		}
	}
}

// cycle runs every task that is not backing off.
func (r *Runner) cycle(ctx context.Context, interval time.Duration, backoffs map[string]*backoff) {
	for _, t := range r.tasks() {
		if ctx.Err() != nil {
			return
		}
		b := backoffs[t.key]
		if b == nil {
			b = &backoff{}
			backoffs[t.key] = b
		}
		if b.skip > 0 {
			b.skip--
			continue
		}
		n, err := r.runTask(ctx, t)
		switch {
		case ctx.Err() != nil:
			return
		case err != nil:
			wait := b.fail(interval)
			r.log().Warn("ingest failed", "source", t.key, "err", err, "failures", b.fails, "retry_in", wait)
		case b.fails > 0:
			r.log().Info("ingest recovered", "source", t.key, "records", n)
			b.reset()
		case n > 0:
			r.log().Info("ingested", "source", t.key, "records", n)
		}
	}
}

// prune deletes data older than Retention.
func (r *Runner) prune(ctx context.Context) error {
	n, err := r.Store.Prune(ctx, r.now().Add(-r.Retention))
	if err != nil {
		return fmt.Errorf("pruning: %w", err)
	}
	if n > 0 {
		r.log().Info("pruned old records", "records", n, "retention", r.Retention)
	}
	return nil
}

// runTask executes t and records its status. It returns the number of
// records ingested (even on error) and the error, prefixed with the source.
func (r *Runner) runTask(ctx context.Context, t task) (int64, error) {
	st := model.SourceStatus{Name: t.key, Kind: t.kind, LastRun: r.now()}
	n, err := t.run(ctx, t.key)
	st.Records = n
	if err != nil {
		err = fmt.Errorf("%s: %w", t.key, err)
		st.LastError = err.Error()
	} else {
		st.LastOK = r.now()
	}
	if rerr := r.Store.RecordRun(ctx, st); rerr != nil {
		err = errors.Join(err, fmt.Errorf("%s: recording status: %w", t.key, rerr))
	}
	return n, err
}

// tasks lists the work for every source. A source's key is its Name, or
// Name/kind when several sources share that name (e.g. one value acting as
// both a DNS and a device source), so statuses and cursors never collide.
func (r *Runner) tasks() []task {
	count := map[string]int{}
	for _, s := range r.DNS {
		count[s.Name()]++
	}
	for _, s := range r.Flows {
		count[s.Name()]++
	}
	for _, s := range r.Devices {
		count[s.Name()]++
	}
	key := func(name, kind string) string {
		if count[name] > 1 {
			return name + "/" + kind
		}
		return name
	}

	var ts []task
	for _, s := range r.DNS {
		ts = append(ts, task{key(s.Name(), "dns"), "dns", func(ctx context.Context, k string) (int64, error) {
			return drain(ctx, r.Store, r.log(), k, r.batch(), s.FetchDNS, r.Store.InsertDNS)
		}})
	}
	for _, s := range r.Flows {
		ts = append(ts, task{key(s.Name(), "flow"), "flow", func(ctx context.Context, k string) (int64, error) {
			return drain(ctx, r.Store, r.log(), k, r.batch(), s.FetchFlows, r.Store.InsertFlows)
		}})
	}
	for _, s := range r.Devices {
		ts = append(ts, task{key(s.Name(), "devices"), "devices", func(ctx context.Context, _ string) (int64, error) {
			ds, err := s.Devices(ctx)
			if err != nil {
				return 0, err
			}
			if len(ds) == 0 {
				return 0, nil
			}
			if err := r.Store.UpsertDevices(ctx, ds); err != nil {
				return 0, err
			}
			return int64(len(ds)), nil
		}})
	}
	return ts
}

// drain fetches and inserts batches until the source has nothing new, the
// per-run cap is reached, or something fails. The cursor is advanced only
// after the batch it covers has been inserted. A saved cursor the source
// cannot read (source.ErrBadCursor) is dropped and reading starts over,
// rather than failing on every run until someone edits the database.
func drain[T any](
	ctx context.Context, st Store, log *slog.Logger, key string, limit int,
	fetch func(context.Context, string, int) ([]T, string, error),
	insert func(context.Context, []T) error,
) (int64, error) {
	cur, err := st.Cursor(ctx, key)
	if err != nil {
		return 0, fmt.Errorf("reading cursor: %w", err)
	}
	var total int64
	for range maxBatchesPerRun {
		if err := ctx.Err(); err != nil {
			return total, err
		}
		recs, next, err := fetch(ctx, cur, limit)
		if errors.Is(err, source.ErrBadCursor) && cur != "" {
			log.Warn("discarding unreadable cursor; reading the source from the start", "source", key, "err", err)
			if err := st.SetCursor(ctx, key, ""); err != nil {
				return total, fmt.Errorf("resetting cursor: %w", err)
			}
			cur = ""
			recs, next, err = fetch(ctx, cur, limit)
		}
		if err != nil {
			return total, fmt.Errorf("fetching: %w", err)
		}
		if len(recs) > 0 {
			if err := insert(ctx, recs); err != nil {
				return total, fmt.Errorf("storing: %w", err)
			}
			total += int64(len(recs))
		}
		if next == cur {
			return total, nil
		}
		if err := st.SetCursor(ctx, key, next); err != nil {
			return total, fmt.Errorf("saving cursor: %w", err)
		}
		cur = next
		if len(recs) < limit {
			return total, nil
		}
	}
	return total, nil
}

func (r *Runner) batch() int {
	if r.Batch > 0 {
		return r.Batch
	}
	return DefaultBatch
}

func (r *Runner) now() time.Time {
	if r.Now != nil {
		return r.Now()
	}
	return time.Now()
}

func (r *Runner) log() *slog.Logger {
	if r.Logger != nil {
		return r.Logger
	}
	return slog.Default()
}

// backoff tracks consecutive failures of one source in units of ticks.
type backoff struct {
	fails int
	skip  int // ticks to sit out before the next attempt
}

// fail records a failure and returns the delay until the next attempt:
// one interval, then doubling (2, 4, 8, ... intervals), capped at maxBackoff.
func (b *backoff) fail(interval time.Duration) time.Duration {
	b.fails++
	limit := max(int(maxBackoff/interval), 1)
	ticks := 1
	for i := 1; i < b.fails && ticks < limit; i++ {
		ticks *= 2
	}
	ticks = min(ticks, limit)
	b.skip = ticks - 1
	return time.Duration(ticks) * interval
}

func (b *backoff) reset() { *b = backoff{} }
