package ingest

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net/netip"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/bizpers11991-code/phonehome/internal/model"
	"github.com/bizpers11991-code/phonehome/internal/source"
)

// fakeStore is an in-memory Store that can be told to fail.
type fakeStore struct {
	mu        sync.Mutex
	dns       []model.DNSQuery
	flows     []model.Flow
	devices   []model.Device
	cursors   map[string]string
	runs      map[string]model.SourceStatus
	pruned    []time.Time
	failDNS   error
	failSetCx error
}

func newStore() *fakeStore {
	return &fakeStore{cursors: map[string]string{}, runs: map[string]model.SourceStatus{}}
}

func (s *fakeStore) InsertDNS(_ context.Context, qs []model.DNSQuery) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.failDNS != nil {
		return s.failDNS
	}
	s.dns = append(s.dns, qs...)
	return nil
}

func (s *fakeStore) InsertFlows(_ context.Context, fs []model.Flow) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.flows = append(s.flows, fs...)
	return nil
}

func (s *fakeStore) UpsertDevices(_ context.Context, ds []model.Device) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.devices = append(s.devices, ds...)
	return nil
}

func (s *fakeStore) Cursor(_ context.Context, src string) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.cursors[src], nil
}

func (s *fakeStore) SetCursor(_ context.Context, src, c string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.failSetCx != nil {
		return s.failSetCx
	}
	s.cursors[src] = c
	return nil
}

func (s *fakeStore) RecordRun(_ context.Context, st model.SourceStatus) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	prev := s.runs[st.Name]
	st.Records += prev.Records
	if st.LastOK.IsZero() {
		st.LastOK = prev.LastOK
	}
	s.runs[st.Name] = st
	return nil
}

func (s *fakeStore) Prune(_ context.Context, before time.Time) (int64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.pruned = append(s.pruned, before)
	return 0, nil
}

// fakeDNS serves total records with integer-offset cursors.
type fakeDNS struct {
	name  string
	total int
	err   error
	calls int
}

func (f *fakeDNS) Name() string { return f.name }

func (f *fakeDNS) FetchDNS(_ context.Context, cursor string, limit int) ([]model.DNSQuery, string, error) {
	f.calls++
	if f.err != nil {
		return nil, cursor, f.err
	}
	off := 0
	if cursor != "" {
		off, _ = strconv.Atoi(cursor)
	}
	end := min(off+limit, f.total)
	var qs []model.DNSQuery
	for i := off; i < end; i++ {
		qs = append(qs, model.DNSQuery{
			Time:     time.Unix(int64(i), 0),
			ClientIP: netip.MustParseAddr("192.168.1.2"),
			Domain:   "example.com",
			Source:   f.name,
		})
	}
	if len(qs) == 0 {
		return nil, cursor, nil
	}
	return qs, strconv.Itoa(end), nil
}

type fakeFlows struct{ name string }

func (f fakeFlows) Name() string { return f.name }

func (f fakeFlows) FetchFlows(_ context.Context, cursor string, _ int) ([]model.Flow, string, error) {
	if cursor == "done" {
		return nil, cursor, nil
	}
	return []model.Flow{{Proto: "tcp", Source: f.name}}, "done", nil
}

type fakeDevices struct {
	name string
	err  error
}

func (f fakeDevices) Name() string { return f.name }

func (f fakeDevices) Devices(context.Context) ([]model.Device, error) {
	if f.err != nil {
		return nil, f.err
	}
	return []model.Device{{ID: "mac:aa:bb:cc:dd:ee:ff"}}, nil
}

var quiet = slog.New(slog.NewTextHandler(io.Discard, nil))

func TestRunOnceDrains(t *testing.T) {
	st := newStore()
	src := &fakeDNS{name: "pihole", total: 25}
	r := &Runner{Store: st, DNS: []source.DNSSource{src}, Batch: 10, Logger: quiet}
	if err := r.RunOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(st.dns) != 25 || st.cursors["pihole"] != "25" {
		t.Fatalf("got %d records, cursor %q", len(st.dns), st.cursors["pihole"])
	}
	if src.calls != 3 {
		t.Errorf("fetch calls = %d, want 3 (10+10+5)", src.calls)
	}
	run := st.runs["pihole"]
	if run.Records != 25 || run.Kind != "dns" || run.LastError != "" || run.LastOK.IsZero() {
		t.Errorf("status = %+v", run)
	}

	// Nothing new: no inserts, cursor unchanged.
	if err := r.RunOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(st.dns) != 25 || st.runs["pihole"].Records != 25 {
		t.Errorf("second run re-ingested: %d records", len(st.dns))
	}
}

func TestRunOnceCapsBacklog(t *testing.T) {
	st := newStore()
	big := &fakeDNS{name: "big", total: 10*maxBatchesPerRun + 5}
	small := &fakeDNS{name: "small", total: 3}
	r := &Runner{Store: st, DNS: []source.DNSSource{big, small}, Batch: 10, Logger: quiet}
	if err := r.RunOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := st.runs["big"].Records; got != 10*maxBatchesPerRun {
		t.Errorf("big ingested %d in one run, want cap %d", got, 10*maxBatchesPerRun)
	}
	if st.runs["small"].Records != 3 {
		t.Errorf("small starved: %+v", st.runs["small"])
	}
	if err := r.RunOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := st.runs["big"].Records; got != 10*maxBatchesPerRun+5 {
		t.Errorf("backlog not finished on next run: %d", got)
	}
}

func TestCursorSavedOnlyAfterInsert(t *testing.T) {
	st := newStore()
	st.failDNS = errors.New("disk full")
	r := &Runner{Store: st, DNS: []source.DNSSource{&fakeDNS{name: "pihole", total: 5}}, Logger: quiet}
	err := r.RunOnce(context.Background())
	if err == nil || !strings.Contains(err.Error(), "disk full") || !strings.Contains(err.Error(), "pihole") {
		t.Fatalf("err = %v", err)
	}
	if c, ok := st.cursors["pihole"]; ok {
		t.Errorf("cursor advanced to %q despite failed insert", c)
	}
	if run := st.runs["pihole"]; run.LastError == "" || !run.LastOK.IsZero() {
		t.Errorf("status = %+v, want error recorded", run)
	}

	st.failDNS = nil
	if err := r.RunOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(st.dns) != 5 || st.cursors["pihole"] != "5" {
		t.Errorf("after recovery: %d records, cursor %q", len(st.dns), st.cursors["pihole"])
	}
	if run := st.runs["pihole"]; run.LastError != "" {
		t.Errorf("error not cleared: %+v", run)
	}
}

func TestErrorIsolation(t *testing.T) {
	st := newStore()
	bad := &fakeDNS{name: "adguard", err: errors.New("permission denied")}
	good := &fakeDNS{name: "pihole", total: 4}
	r := &Runner{
		Store:   st,
		DNS:     []source.DNSSource{bad, good},
		Flows:   []source.FlowSource{fakeFlows{name: "conntrack"}},
		Devices: []source.DeviceSource{fakeDevices{name: "leases", err: errors.New("no such file")}, fakeDevices{name: "pihole"}},
		Logger:  quiet,
	}
	err := r.RunOnce(context.Background())
	for _, want := range []string{"adguard", "permission denied", "leases", "no such file"} {
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("err = %v, want it to mention %q", err, want)
		}
	}
	if len(st.dns) != 4 || len(st.flows) != 1 || len(st.devices) != 1 {
		t.Errorf("healthy sources affected: dns=%d flows=%d devices=%d", len(st.dns), len(st.flows), len(st.devices))
	}
	// "pihole" is both a DNS and a device source, so keys get a kind suffix.
	for _, k := range []string{"pihole/dns", "pihole/devices", "adguard", "conntrack", "leases"} {
		if _, ok := st.runs[k]; !ok {
			t.Errorf("no status for %q (have %v)", k, keys(st.runs))
		}
	}
	if st.runs["pihole/devices"].Kind != "devices" || st.runs["conntrack"].Kind != "flow" {
		t.Errorf("kinds wrong: %+v", st.runs)
	}
}

func TestSetCursorFailureIsReported(t *testing.T) {
	st := newStore()
	st.failSetCx = errors.New("locked")
	r := &Runner{Store: st, DNS: []source.DNSSource{&fakeDNS{name: "pihole", total: 3}}, Logger: quiet}
	if err := r.RunOnce(context.Background()); err == nil || !strings.Contains(err.Error(), "saving cursor") {
		t.Fatalf("err = %v", err)
	}
	if st.runs["pihole"].Records != 3 {
		t.Errorf("inserted records should still be counted: %+v", st.runs["pihole"])
	}
}

func TestBackoff(t *testing.T) {
	var b backoff
	var got []time.Duration
	for range 8 {
		got = append(got, b.fail(time.Minute))
	}
	want := []time.Duration{1, 2, 4, 8, 15, 15, 15, 15}
	for i := range want {
		if got[i] != want[i]*time.Minute {
			t.Fatalf("delays = %v, want %v minutes", got, want)
		}
	}
	b.reset()
	if b.fails != 0 || b.skip != 0 {
		t.Errorf("reset: %+v", b)
	}
	if d := (&backoff{fails: 30}).fail(time.Second); d > maxBackoff {
		t.Errorf("delay %v exceeds cap", d)
	}
}

func TestCycleSkipsFailingSource(t *testing.T) {
	st := newStore()
	bad := &fakeDNS{name: "bad", err: errors.New("down")}
	good := &fakeDNS{name: "good"}
	r := &Runner{Store: st, DNS: []source.DNSSource{bad, good}, Logger: quiet}
	backoffs := map[string]*backoff{}
	for range 10 {
		r.cycle(context.Background(), time.Minute, backoffs)
	}
	if good.calls != 10 {
		t.Errorf("healthy source called %d times, want 10", good.calls)
	}
	// Attempts at ticks 1, 2, 4, 8 (waits of 1, 2, 4 intervals).
	if bad.calls != 4 {
		t.Errorf("failing source called %d times in 10 ticks, want 4", bad.calls)
	}

	bad.err = nil
	for range 8 {
		r.cycle(context.Background(), time.Minute, backoffs)
	}
	if backoffs["bad"].fails != 0 {
		t.Errorf("backoff not reset after recovery: %+v", backoffs["bad"])
	}
}

func TestRunPrunesAndStops(t *testing.T) {
	st := newStore()
	now := time.Date(2026, 1, 10, 12, 0, 0, 0, time.UTC)
	r := &Runner{
		Store:     st,
		DNS:       []source.DNSSource{&fakeDNS{name: "pihole", total: 2}},
		Retention: 48 * time.Hour,
		Logger:    quiet,
		Now:       func() time.Time { return now },
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		r.Run(ctx, time.Millisecond)
		close(done)
	}()
	deadline := time.After(5 * time.Second)
	for {
		st.mu.Lock()
		n := len(st.dns)
		st.mu.Unlock()
		if n == 2 {
			break
		}
		select {
		case <-deadline:
			t.Fatal("Run did not ingest")
		case <-time.After(time.Millisecond):
		}
	}
	time.Sleep(20 * time.Millisecond) // several ticks; clock is frozen so no second prune
	cancel()
	<-done
	st.mu.Lock()
	defer st.mu.Unlock()
	if len(st.pruned) != 1 || !st.pruned[0].Equal(now.Add(-48*time.Hour)) {
		t.Errorf("pruned = %v, want exactly once at now-48h", st.pruned)
	}
}

func keys(m map[string]model.SourceStatus) []string {
	var ks []string
	for k := range m {
		ks = append(ks, k)
	}
	return ks
}
