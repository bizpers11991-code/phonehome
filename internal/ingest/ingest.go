// Package ingest moves observations from sources into the store.
//
// A batch and the cursor that covers it are written in one transaction
// (InsertDNSWithCursor, InsertFlowsWithCursor), so a crash, a kill -9 or a
// shutdown in the middle of a run neither loses a batch nor stores it twice
// on the next start.
package ingest

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"time"

	"github.com/bizpers11991-code/phonehome/internal/model"
	"github.com/bizpers11991-code/phonehome/internal/source"
)

// Store is the subset of the store that ingestion writes to.
type Store interface {
	InsertDNSWithCursor(ctx context.Context, qs []model.DNSQuery, source, cursor string) error
	InsertFlowsWithCursor(ctx context.Context, fs []model.Flow, source, cursor string) error
	UpsertDevices(ctx context.Context, ds []model.Device) error
	Cursor(ctx context.Context, source string) (string, error)
	SetCursor(ctx context.Context, source, cursor string) error
	RecordRun(ctx context.Context, st model.SourceStatus) error
	Prune(ctx context.Context, before time.Time) (int64, error)
}

const (
	// DefaultBatch is the fetch size used when Runner.Batch is zero.
	DefaultBatch = 5000
	// maxBatchesPerRun caps one source's work per round so a huge backlog
	// cannot starve the others; the rest is picked up in the next round,
	// which starts straight away while any source still has a backlog.
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

	hinted bool // the "file will not shrink" hint has been logged
}

// task is one source's unit of work in a run. run reports whether the
// source stopped at the per-round cap with more waiting.
type task struct {
	key  string // status name and cursor key
	kind string // "dns", "flow", "devices"
	run  func(ctx context.Context, key string) (n int64, more bool, err error)
}

// RunOnce ingests everything the sources have, in rounds of at most
// maxBatchesPerRun batches per source, then prunes data older than
// Retention (when set), so that `ingest --once` from cron keeps the store
// bounded too. Sources are independent: a failing source is recorded and
// skipped, and the errors of all failing sources are returned joined.
func (r *Runner) RunOnce(ctx context.Context) error {
	var errs []error
	for ts := r.tasks(); len(ts) > 0; {
		var again []task
		for _, t := range ts {
			if err := ctx.Err(); err != nil {
				return errors.Join(append(errs, err)...)
			}
			_, more, err := r.runTask(ctx, t)
			switch {
			case err != nil:
				errs = append(errs, err)
			case more:
				again = append(again, t)
			}
		}
		ts = again
	}
	if r.Retention > 0 {
		if err := r.prune(ctx); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

// Run ingests every interval until ctx is cancelled. While a source still
// has a backlog after its capped round (the first start on months of
// history), the next round starts at once instead of an interval later. A
// source that keeps failing is retried with exponential backoff (capped at
// 15 minutes), and old data is pruned once a day when Retention is set.
func (r *Runner) Run(ctx context.Context, interval time.Duration) {
	tick := time.NewTicker(interval)
	defer tick.Stop()
	backoffs := map[string]*backoff{}
	var lastPrune time.Time
	ticked := true
	for {
		more := r.cycle(ctx, interval, backoffs, ticked)
		// Prune once caught up, or old history read in later rounds of
		// the catch-up would outstay the retention by a day.
		if !more && r.Retention > 0 && r.now().Sub(lastPrune) >= pruneEvery {
			lastPrune = r.now()
			if err := r.prune(ctx); err != nil {
				r.log().Warn("prune failed", "err", err)
			}
		}
		if more {
			if ctx.Err() != nil {
				return
			}
			ticked = false
			continue
		}
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
			ticked = true
		}
	}
}

// cycle runs every task that is not backing off, and reports whether one
// of them stopped at the per-round cap with more waiting. Only a cycle
// started by a tick (ticked) counts down backoffs and retries a source that
// has been failing; the extra rounds of a catch-up leave those to the ticks.
func (r *Runner) cycle(ctx context.Context, interval time.Duration, backoffs map[string]*backoff, ticked bool) (more bool) {
	for _, t := range r.tasks() {
		if ctx.Err() != nil {
			return false
		}
		b := backoffs[t.key]
		if b == nil {
			b = &backoff{}
			backoffs[t.key] = b
		}
		if !ticked && b.fails > 0 {
			continue
		}
		if b.skip > 0 {
			b.skip--
			continue
		}
		n, again, err := r.runTask(ctx, t)
		switch {
		case ctx.Err() != nil:
			return false
		case err != nil:
			wait := b.fail(interval)
			r.log().Warn("ingest failed", "source", t.key, "err", err, "failures", b.fails, "retry_in", wait)
		case b.fails > 0:
			r.log().Info("ingest recovered", "source", t.key, "records", n)
			b.reset()
		case n > 0 && t.kind != "devices": // those are re-read every time
			r.log().Info("ingested", "source", t.key, "records", n)
		}
		more = more || err == nil && again
	}
	return more
}

// prune deletes data older than Retention.
func (r *Runner) prune(ctx context.Context) error {
	n, err := r.Store.Prune(ctx, r.now().Add(-r.Retention))
	if err != nil {
		return fmt.Errorf("pruning: %w", err)
	}
	if n > 0 {
		r.log().Info("pruned old records", "records", n, "retention", r.Retention)
		if av, ok := r.Store.(interface{ AutoVacuum() bool }); ok && !av.AutoVacuum() && !r.hinted {
			r.hinted = true
			r.log().Info("the database file will not shrink: it was created before incremental vacuum was set up, " +
				"so freed space is only reused. To shrink it once, stop phonehome and run: " +
				"sqlite3 <database> 'PRAGMA auto_vacuum=INCREMENTAL; VACUUM;'")
		}
	}
	return nil
}

// runTask executes t and records its status. It returns the number of
// records ingested (even on error), whether more are waiting, and the
// error, prefixed with the source.
func (r *Runner) runTask(ctx context.Context, t task) (int64, bool, error) {
	st := model.SourceStatus{Name: t.key, Kind: t.kind, LastRun: r.now()}
	n, more, err := t.run(ctx, t.key)
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
	return n, more, err
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
		ts = append(ts, task{key(s.Name(), "dns"), "dns", func(ctx context.Context, k string) (int64, bool, error) {
			return drain(ctx, r.Store, r.log(), k, r.batch(), r.cutoff(), func(q model.DNSQuery) time.Time { return q.Time },
				s.FetchDNS, r.Store.InsertDNSWithCursor)
		}})
	}
	for _, s := range r.Flows {
		ts = append(ts, task{key(s.Name(), "flow"), "flow", func(ctx context.Context, k string) (int64, bool, error) {
			return drain(ctx, r.Store, r.log(), k, r.batch(), r.cutoff(), func(f model.Flow) time.Time { return f.Start },
				s.FetchFlows, r.Store.InsertFlowsWithCursor)
		}})
	}
	for _, s := range r.Devices {
		ts = append(ts, task{key(s.Name(), "devices"), "devices", func(ctx context.Context, _ string) (int64, bool, error) {
			ds, err := s.Devices(ctx)
			if err != nil {
				return 0, false, err
			}
			if len(ds) == 0 {
				return 0, false, nil
			}
			if err := r.Store.UpsertDevices(ctx, ds); err != nil {
				return 0, false, err
			}
			return int64(len(ds)), false, nil
		}})
	}
	return ts
}

// cutoff is the oldest time worth storing: what Prune would keep. The zero
// time (no retention) keeps everything.
func (r *Runner) cutoff() time.Time {
	if r.Retention <= 0 {
		return time.Time{}
	}
	return r.now().Add(-r.Retention)
}

// drain fetches and inserts batches until the source has nothing new, the
// per-round cap is reached (more is then true), or something fails. Records
// older than since (when set) are dropped before they are stored: Prune
// would delete them at once, and a first read of a long history would
// otherwise write millions of rows only to delete them. Sources told the
// cutoff (source.WithSince) may skip them earlier. A batch that is dropped
// entirely still advances the cursor. The cursor is advanced only
// after the batch it covers has been inserted. A saved cursor the source
// cannot read (source.ErrBadCursor) is dropped and reading starts over,
// rather than failing on every run until someone edits the database.
func drain[T any](
	ctx context.Context, st Store, log *slog.Logger, key string, limit int,
	since time.Time, at func(T) time.Time,
	fetch func(context.Context, string, int) ([]T, string, error),
	insert func(ctx context.Context, recs []T, key, cursor string) error,
) (int64, bool, error) {
	cur, err := st.Cursor(ctx, key)
	if err != nil {
		return 0, false, fmt.Errorf("reading cursor: %w", err)
	}
	if !since.IsZero() {
		ctx = source.WithSince(ctx, since)
	}
	var total int64
	for range maxBatchesPerRun {
		if err := ctx.Err(); err != nil {
			return total, false, err
		}
		recs, next, err := fetch(ctx, cur, limit)
		if errors.Is(err, source.ErrBadCursor) && cur != "" {
			log.Warn("discarding unreadable cursor; reading the source from the start", "source", key, "err", err)
			if err := st.SetCursor(ctx, key, ""); err != nil {
				return total, false, fmt.Errorf("resetting cursor: %w", err)
			}
			cur = ""
			recs, next, err = fetch(ctx, cur, limit)
		}
		if err != nil {
			return total, false, fmt.Errorf("fetching: %w", err)
		}
		if next == cur && len(recs) == 0 {
			return total, false, nil
		}
		fetched := len(recs)
		if !since.IsZero() {
			recs = slices.DeleteFunc(recs, func(rec T) bool { return at(rec).Before(since) })
		}
		// The batch and its cursor go in together, so that a crash cannot
		// leave the batch stored with the old cursor and read again.
		if len(recs) > 0 {
			if err := insert(ctx, recs, key, next); err != nil {
				return total, false, fmt.Errorf("storing: %w", err)
			}
			total += int64(len(recs))
		} else if err := st.SetCursor(ctx, key, next); err != nil {
			return total, false, fmt.Errorf("saving cursor: %w", err)
		}
		if next == cur {
			return total, false, nil
		}
		cur = next
		if fetched < limit {
			return total, false, nil
		}
	}
	return total, true, nil
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
