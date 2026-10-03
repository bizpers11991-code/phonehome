package alert

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/bizpers11991-code/phonehome/internal/model"
)

type memStore struct {
	mu     sync.Mutex
	m      map[string]string
	writes int
}

func (s *memStore) AlertState(_ context.Context, k string) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.m[k], nil
}

func (s *memStore) SetAlertState(_ context.Context, k, v string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.m == nil {
		s.m = map[string]string{}
	}
	s.writes++
	s.m[k] = v
	return nil
}

type recorder struct {
	batches []Batch
	err     error
}

func (r *recorder) Name() string { return "recorder" }

func (r *recorder) Notify(_ context.Context, b Batch) error {
	r.batches = append(r.batches, b)
	return r.err
}

// home is a test fixture: the report the engine will see next.
type home struct {
	now  time.Time
	devs []model.DeviceReport
	demo bool
}

func (h *home) report(_ context.Context, p model.Period) (model.HomeReport, error) {
	if !p.To.Equal(h.now) || p.To.Sub(p.From) != Window {
		return model.HomeReport{}, errors.New("unexpected period")
	}
	return model.HomeReport{Period: p, Devices: h.devs, Demo: h.demo}, nil
}

func dev(id, name, grade string) model.DeviceReport {
	return model.DeviceReport{Device: model.Device{ID: id, Label: name, Kind: model.KindTV, Vendor: "Samsung"}, Grade: grade}
}

func newEngine(h *home) (*Engine, *recorder, *memStore) {
	rec, st := &recorder{}, &memStore{}
	e := &Engine{Report: h.report, Store: st, Notifiers: []Notifier{rec}, Now: func() time.Time { return h.now }}
	return e, rec, st
}

func check(t *testing.T, e *Engine) {
	t.Helper()
	if err := e.Check(context.Background()); err != nil {
		t.Fatal(err)
	}
}

func kinds(b Batch) string {
	var k []string
	for _, e := range b.Events {
		k = append(k, e.Kind+":"+e.Device.Name)
	}
	return strings.Join(k, " ")
}

func TestEngine(t *testing.T) {
	h := &home{now: time.Date(2026, 10, 2, 12, 0, 0, 0, time.UTC), devs: []model.DeviceReport{dev("mac:1", "TV", "B")}}
	e, rec, st := newEngine(h)

	check(t, e) // the first check records a baseline and says nothing
	if len(rec.batches) != 0 {
		t.Fatalf("baseline sent %v", rec.batches)
	}

	tick := func() { h.now = h.now.Add(5 * time.Minute) }
	tick()
	tv := dev("mac:1", "TV", "D")
	tv.Heartbeats = []model.Heartbeat{
		{Domain: "acr.example", Category: model.CatACR, Every: time.Minute},
		{Domain: "time.example", Category: model.CatEssential, Every: time.Hour}, // not snooping: no alert
	}
	tv.Bypasses = []model.Bypass{{Kind: "doh-flow", Detail: "Connected to 8.8.8.8:443.", Evidence: "8.8.8.8:443", Confidence: "high"}}
	h.devs = []model.DeviceReport{tv, dev("mac:2", "Plug", "A")}
	check(t, e)
	if len(rec.batches) != 1 {
		t.Fatalf("got %d batches, want 1", len(rec.batches))
	}
	if got, want := kinds(rec.batches[0]), "grade_worse:TV heartbeat:TV bypass:TV new_device:Plug"; got != want {
		t.Fatalf("events %q, want %q", got, want)
	}
	if ev := rec.batches[0].Events[0]; ev.Before != "B" || ev.Grade != "D" || ev.Device.Vendor != "Samsung" {
		t.Errorf("grade event %+v", ev)
	}

	// Nothing changed: nothing is sent, and nothing is written.
	tick()
	writes := st.writes
	check(t, e)
	if len(rec.batches) != 1 {
		t.Fatalf("unchanged report sent a batch")
	}
	if st.writes != writes {
		t.Fatalf("unchanged state written again")
	}

	// Better, then worse again within a day: the same alert is not repeated.
	tick()
	h.devs = []model.DeviceReport{dev("mac:1", "TV", "B"), dev("mac:2", "Plug", "A")}
	check(t, e)
	tick()
	h.devs[0] = tv
	check(t, e)
	if len(rec.batches) != 1 {
		t.Fatalf("repeated alert within the cooldown: %v", kinds(rec.batches[len(rec.batches)-1]))
	}
	// A day later it is news again.
	h.now = h.now.Add(cooldown)
	h.devs = []model.DeviceReport{dev("mac:1", "TV", "B"), dev("mac:2", "Plug", "A")}
	check(t, e)
	tick()
	h.devs[0] = dev("mac:1", "TV", "D")
	check(t, e)
	if len(rec.batches) != 2 || kinds(rec.batches[1]) != "grade_worse:TV" {
		t.Fatalf("after the cooldown: %d batches", len(rec.batches))
	}

	// A device missing from this week's report is not new when it returns.
	tick()
	h.devs = h.devs[:1]
	check(t, e)
	tick()
	h.devs = append(h.devs, dev("mac:2", "Plug", "A"))
	check(t, e)
	if len(rec.batches) != 2 {
		t.Fatalf("a returning device was announced as new")
	}
}

func TestEngineRateLimit(t *testing.T) {
	h := &home{now: time.Date(2026, 10, 2, 12, 0, 0, 0, time.UTC), devs: []model.DeviceReport{dev("mac:0", "First", "A")}}
	e, rec, _ := newEngine(h)
	e.MaxPerHour = 2
	check(t, e)
	for i := range 4 {
		h.now = h.now.Add(time.Minute)
		h.devs = append(h.devs, dev("mac:"+string(rune('a'+i)), "D"+string(rune('a'+i)), "A"))
		check(t, e)
	}
	if len(rec.batches) != 2 {
		t.Fatalf("sent %d batches in an hour, limit 2", len(rec.batches))
	}
	// Held-back events are found again once the hour allows.
	h.now = h.now.Add(time.Hour)
	check(t, e)
	if len(rec.batches) != 3 || kinds(rec.batches[2]) != "new_device:Dc new_device:Dd" {
		t.Fatalf("held-back events: %d batches, last %q", len(rec.batches), kinds(rec.batches[len(rec.batches)-1]))
	}
}

func TestEngineWantsAndFailures(t *testing.T) {
	h := &home{now: time.Date(2026, 10, 2, 12, 0, 0, 0, time.UTC), devs: []model.DeviceReport{dev("mac:1", "TV", "A")}, demo: true}
	e, rec, st := newEngine(h)
	e.Wants = func(k string) bool { return k == NewDevice }
	rec.err = errors.New("down")
	check(t, e)
	h.now = h.now.Add(time.Minute)
	h.devs = []model.DeviceReport{dev("mac:1", "TV", "F"), dev("mac:2", "Plug", "A")}
	check(t, e) // a failing target is logged, not fatal
	if len(rec.batches) != 1 || kinds(rec.batches[0]) != "new_device:Plug" || !rec.batches[0].Demo {
		t.Fatalf("got %+v", rec.batches)
	}
	// Delivery is at most once: the failed batch is not sent again.
	h.now = h.now.Add(time.Minute)
	check(t, e)
	if len(rec.batches) != 1 {
		t.Fatalf("failed batch was resent")
	}

	// A damaged state is replaced by a new baseline.
	st.m[stateKey] = "{"
	h.devs = append(h.devs, dev("mac:3", "Cam", "A"))
	check(t, e)
	if len(rec.batches) != 1 {
		t.Fatalf("a fresh baseline sent alerts")
	}
}

func TestEngineWaitsForData(t *testing.T) {
	h := &home{now: time.Date(2026, 10, 2, 12, 0, 0, 0, time.UTC)}
	e, rec, st := newEngine(h)
	check(t, e) // nothing ingested yet: no baseline
	if st.m[stateKey] != "" {
		t.Fatalf("baseline taken with no devices: %s", st.m[stateKey])
	}
	h.now = h.now.Add(5 * time.Minute)
	h.devs = []model.DeviceReport{dev("mac:1", "TV", "B"), dev("mac:2", "Plug", "A")}
	check(t, e) // the first data is the baseline, not a flood of new devices
	if len(rec.batches) != 0 {
		t.Fatalf("sent %v", kinds(rec.batches[0]))
	}
}

func TestEngineKnownDevices(t *testing.T) {
	h := &home{now: time.Date(2026, 10, 2, 12, 0, 0, 0, time.UTC), devs: []model.DeviceReport{dev("mac:1", "TV", "B")}}
	e, rec, st := newEngine(h)
	known := []model.Device{{ID: "mac:1"}, {ID: "mac:9"}, {ID: "mac:8"}} // mac:9 and mac:8 were quiet this week
	e.Devices = func(context.Context) ([]model.Device, error) { return known, nil }
	check(t, e)
	h.now = h.now.Add(5 * time.Minute)
	quiet := dev("mac:9", "Old camera", "F")
	quiet.Heartbeats = []model.Heartbeat{{Domain: "t.example", Category: model.CatTelemetry}}
	h.devs = append(h.devs, quiet)
	check(t, e) // it wakes up: not new, and its state is learnt silently
	if len(rec.batches) != 0 {
		t.Fatalf("sent %q", kinds(rec.batches[0]))
	}
	h.now = h.now.Add(5 * time.Minute)
	h.devs[0] = dev("mac:1", "TV", "D")
	check(t, e)
	if len(rec.batches) != 1 || kinds(rec.batches[0]) != "grade_worse:TV" {
		t.Fatalf("got %d batches", len(rec.batches))
	}

	// A device known only by IP is forgotten some months after it was last
	// seen; one the store keeps (mac:8, never active) is not.
	h.now = h.now.Add(5 * time.Minute)
	h.devs = append(h.devs, dev("ip:10.0.0.5", "Guest", "A"))
	check(t, e)
	h.devs = h.devs[:2]
	h.now = h.now.Add(forgetDevice + 48*time.Hour)
	check(t, e)
	if s := st.m[stateKey]; strings.Contains(s, "ip:10.0.0.5") || !strings.Contains(s, "mac:8") || !strings.Contains(s, "mac:9") {
		t.Fatalf("state %s", s)
	}
}

type failingStore struct{ memStore }

func (s *failingStore) SetAlertState(context.Context, string, string) error {
	return errors.New("disk full")
}

// TestEngineSavesFirst: state is saved before a batch goes out, so a
// database that cannot be written means no alerts, not the same alerts
// on every check.
func TestEngineSavesFirst(t *testing.T) {
	h := &home{now: time.Date(2026, 10, 2, 12, 0, 0, 0, time.UTC), devs: []model.DeviceReport{dev("mac:1", "TV", "B")}}
	e, rec, st := newEngine(h)
	check(t, e)
	fs := &failingStore{}
	fs.m = st.m
	e.Store = fs
	h.now = h.now.Add(5 * time.Minute)
	h.devs = append(h.devs, dev("mac:2", "Plug", "A"))
	if err := e.Check(context.Background()); err == nil {
		t.Fatal("no error from a failing store")
	}
	if len(rec.batches) != 0 {
		t.Fatal("sent alerts it could not record")
	}
}

func TestText(t *testing.T) {
	b := Batch{Demo: true, Events: []Event{{Kind: GradeWorse, Device: Device{Name: "TV"}, Grade: "D", Before: "B"}}}
	title, body := Text(b)
	if title != "[DEMO DATA] phonehome: TV is now graded D" || body != "TV got worse: grade B → D." {
		t.Fatalf("%q / %q", title, body)
	}
	var many []Event
	for range 25 {
		many = append(many, Event{Kind: Heartbeat, Device: Device{Name: "TV"}, Domain: "acr.example", Category: model.CatACR, Every: 90 * time.Second})
	}
	title, body = Text(Batch{Events: many})
	lines := strings.Split(body, "\n")
	if title != "phonehome: 25 changes" || len(lines) != maxLines+1 || lines[0] != "TV started contacting acr.example every 2m (Content recognition)." ||
		lines[maxLines] != "…and 5 more; see the dashboard." {
		t.Fatalf("%q\n%s", title, body)
	}
}
