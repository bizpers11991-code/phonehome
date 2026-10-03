// Package alert notifies the user's own services when something changes:
// a new device appears, a device's grade gets worse, a device starts a new
// snooping heartbeat, or it starts bypassing DNS.
//
// The Engine compares each report with what it saw last time. That
// snapshot, which alerts were sent and when, lives in the store, so a
// restart neither repeats nor loses alerts. The very first check only
// records a baseline, once there is data: installing phonehome does not
// announce every device.
//
// An alert never says more than the dashboard shows: device id, name, kind
// and vendor, grades, and the domain or finding that triggered it. The
// exact fields are listed in docs/alerts.md.
package alert

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"time"

	"github.com/bizpers11991-code/phonehome/internal/model"
)

// Event kinds; the same names as the alerts.events config setting.
const (
	NewDevice  = "new_device"
	GradeWorse = "grade_worse"
	Heartbeat  = "heartbeat"
	Bypass     = "bypass"
)

// Window is the period each check looks at: the dashboard's default week,
// so an alert's grade is the grade the dashboard shows.
const Window = 7 * 24 * time.Hour

// cooldown is how long the same alert (same device and the same grade,
// domain or finding) is not repeated, even if it flaps.
const cooldown = 24 * time.Hour

// forgetSent is how long a sent alert is remembered for de-duplication.
const forgetSent = 30 * 24 * time.Hour

// Event is one thing worth telling the user about.
type Event struct {
	Kind   string
	Device Device
	Grade  string // new_device, grade_worse: the grade now
	Before string // grade_worse: the grade before

	// heartbeat
	Domain   string
	Category model.Category
	Every    time.Duration

	// bypass
	Finding model.Bypass
}

// Device is how an alert names a device: what the dashboard shows.
type Device struct {
	ID     string
	Name   string
	Kind   model.DeviceKind
	Vendor string
}

// Batch is what one check sends: every new event, at once.
type Batch struct {
	Time   time.Time
	Demo   bool // built from the synthetic demo household
	Events []Event
}

// Notifier delivers a batch to one service.
type Notifier interface {
	Name() string
	Notify(ctx context.Context, b Batch) error
}

// Publisher receives every report, not only changes. The MQTT target uses
// it to keep per-device grade sensors current.
type Publisher interface {
	Publish(ctx context.Context, r model.HomeReport) error
}

// Store keeps the engine's memory between checks.
type Store interface {
	AlertState(ctx context.Context, key string) (string, error)
	SetAlertState(ctx context.Context, key, value string) error
}

// Engine runs the checks.
type Engine struct {
	Report func(ctx context.Context, p model.Period) (model.HomeReport, error)
	// Devices lists every device phonehome knows, active this week or not.
	// The baseline includes them, so a device that was quiet when alerts
	// were turned on is not announced as new when it wakes up. nil: only
	// the week's devices.
	Devices    func(ctx context.Context) ([]model.Device, error)
	Store      Store
	Notifiers  []Notifier
	Publishers []Publisher
	Wants      func(kind string) bool // nil: every kind
	MaxPerHour int                    // notification batches per hour; 0 = 6
	Now        func() time.Time       // nil: time.Now
	Logger     *slog.Logger           // nil: slog.Default()
}

const stateKey = "state"

// state is what the engine remembers, stored as JSON.
type state struct {
	Devices map[string]*deviceState `json:"devices"` // nil until the baseline is taken
	Sent    map[string]time.Time    `json:"sent"`    // alert key → when it was last sent
	Batches []time.Time             `json:"batches"` // batches sent in the last hour
}

type deviceState struct {
	// Pending marks a device known when the baseline was taken but quiet
	// that week: its state is learnt, silently, when it is next active.
	Pending    bool     `json:"pending,omitempty"`
	Grade      string   `json:"grade"`
	Heartbeats []string `json:"heartbeats"` // snooping heartbeat domains
	Bypasses   []string `json:"bypasses"`   // kind|evidence
}

// Run checks every interval until ctx is cancelled. The first check waits
// one interval, so that ingestion has caught up when a baseline is taken.
func (e *Engine) Run(ctx context.Context, interval time.Duration) {
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
		if err := e.Check(ctx); err != nil && ctx.Err() == nil {
			e.log().Warn("alert check failed", "err", err)
		}
	}
}

// Check compares the current report with the last one and notifies about
// what is new. A batch held back by the hourly limit is not lost: the
// snapshot is left as it was, so the same events are found again next time.
func (e *Engine) Check(ctx context.Context) error {
	now := e.now()
	rep, err := e.Report(ctx, model.Period{From: now.Add(-Window), To: now})
	if err != nil {
		return fmt.Errorf("alerts: report: %w", err)
	}
	st, raw, err := e.load(ctx)
	if err != nil {
		return err
	}
	for _, p := range e.Publishers {
		if err := p.Publish(ctx, rep); err != nil {
			e.log().Warn("alert publish failed", "err", err)
		}
	}

	baseline := st.Devices == nil
	if baseline && len(rep.Devices) == 0 {
		// Nothing ingested yet: a baseline now would make every device
		// "new" later.
		return nil
	}
	var known map[string]bool // every device phonehome knows; nil: unknown
	if e.Devices != nil {
		ds, err := e.Devices(ctx)
		if err != nil {
			return fmt.Errorf("alerts: devices: %w", err)
		}
		known = map[string]bool{}
		for _, d := range ds {
			known[d.ID] = true
		}
	}
	next := map[string]*deviceState{}
	for id, d := range st.Devices {
		// Devices not seen this week keep what we knew, unless phonehome
		// no longer knows them at all.
		if known == nil || known[id] {
			next[id] = d
		}
	}
	if baseline {
		for id := range known {
			next[id] = &deviceState{Pending: true}
		}
	}
	var events []Event
	keys := map[int]string{}
	add := func(key string, ev Event) {
		if e.Wants != nil && !e.Wants(ev.Kind) {
			return
		}
		if t, ok := st.Sent[key]; ok && now.Sub(t) < cooldown {
			return
		}
		keys[len(events)] = key
		events = append(events, ev)
	}
	for _, d := range rep.Devices {
		cur := snapshot(d)
		next[d.Device.ID] = cur
		if baseline {
			continue
		}
		dev := device(d.Device)
		prev := st.Devices[d.Device.ID]
		if prev == nil {
			add(NewDevice+":"+dev.ID, Event{Kind: NewDevice, Device: dev, Grade: d.Grade})
			continue
		}
		if prev.Pending {
			continue // known from the baseline, first seen active now: learn it
		}
		if worse(cur.Grade, prev.Grade) {
			add(GradeWorse+":"+dev.ID+":"+cur.Grade, Event{Kind: GradeWorse, Device: dev, Grade: cur.Grade, Before: prev.Grade})
		}
		for _, h := range d.Heartbeats {
			if h.Category.Snooping() && !slices.Contains(prev.Heartbeats, h.Domain) {
				add(Heartbeat+":"+dev.ID+":"+h.Domain, Event{Kind: Heartbeat, Device: dev, Domain: h.Domain, Category: h.Category, Every: h.Every})
			}
		}
		for _, b := range d.Bypasses {
			if k := bypassKey(b); !slices.Contains(prev.Bypasses, k) {
				add(Bypass+":"+dev.ID+":"+k, Event{Kind: Bypass, Device: dev, Finding: b})
			}
		}
	}

	if len(events) > 0 {
		st.Batches = slices.DeleteFunc(st.Batches, func(t time.Time) bool { return now.Sub(t) >= time.Hour || t.After(now) })
		if len(st.Batches) >= e.maxPerHour() {
			e.log().Info("alerts held back by max_per_hour; they are sent when the hour allows", "events", len(events))
			return nil
		}
		b := Batch{Time: now, Demo: rep.Demo, Events: events}
		var errs []error
		for _, n := range e.Notifiers {
			if err := n.Notify(ctx, b); err != nil {
				errs = append(errs, fmt.Errorf("%s: %w", n.Name(), err))
			}
		}
		if err := errors.Join(errs...); err != nil {
			// Delivery is at most once: a target that is down misses this
			// batch rather than receiving it again and again.
			e.log().Warn("alert delivery failed", "err", err)
		}
		for i := range events {
			st.Sent[keys[i]] = now
		}
		st.Batches = append(st.Batches, now)
		e.log().Info("alerts sent", "events", len(events), "targets", len(e.Notifiers))
	} else if baseline {
		e.log().Info("alerts: recorded a baseline of the devices seen this week; changes from now on are notified", "devices", len(rep.Devices))
	}
	for k, t := range st.Sent {
		if now.Sub(t) > forgetSent {
			delete(st.Sent, k)
		}
	}
	st.Devices = next
	return e.save(ctx, st, raw)
}

func snapshot(d model.DeviceReport) *deviceState {
	s := &deviceState{Grade: d.Grade, Heartbeats: []string{}, Bypasses: []string{}}
	for _, h := range d.Heartbeats {
		if h.Category.Snooping() {
			s.Heartbeats = append(s.Heartbeats, h.Domain)
		}
	}
	for _, b := range d.Bypasses {
		s.Bypasses = append(s.Bypasses, bypassKey(b))
	}
	slices.Sort(s.Heartbeats)
	slices.Sort(s.Bypasses)
	return s
}

func bypassKey(b model.Bypass) string { return b.Kind + "|" + b.Evidence }

func device(d model.Device) Device {
	k := d.Kind
	if k == "" {
		k = model.KindUnknown
	}
	return Device{ID: d.ID, Name: d.DisplayName(), Kind: k, Vendor: d.Vendor}
}

var gradeRank = map[string]int{"A": 0, "B": 1, "C": 2, "D": 3, "F": 4}

// worse reports whether grade now is worse than before; unknown grades
// never are.
func worse(now, before string) bool {
	n, ok1 := gradeRank[now]
	b, ok2 := gradeRank[before]
	return ok1 && ok2 && n > b
}

// load returns the stored state and its JSON as stored.
func (e *Engine) load(ctx context.Context) (*state, string, error) {
	st := &state{}
	raw, err := e.Store.AlertState(ctx, stateKey)
	if err != nil {
		return nil, "", err
	}
	if raw != "" {
		if err := json.Unmarshal([]byte(raw), st); err != nil {
			// A damaged state is replaced by a fresh baseline rather than
			// stopping alerts for good.
			e.log().Warn("alerts: stored state unreadable; taking a new baseline", "err", err)
			st = &state{}
		}
	}
	if st.Sent == nil {
		st.Sent = map[string]time.Time{}
	}
	return st, raw, nil
}

// save stores st unless it is what was loaded: most checks change nothing,
// and the database need not be written every few minutes for that.
func (e *Engine) save(ctx context.Context, st *state, raw string) error {
	b, err := json.Marshal(st)
	if err != nil {
		return err
	}
	if string(b) == raw {
		return nil
	}
	return e.Store.SetAlertState(ctx, stateKey, string(b))
}

func (e *Engine) now() time.Time {
	if e.Now != nil {
		return e.Now()
	}
	return time.Now()
}

func (e *Engine) log() *slog.Logger {
	if e.Logger != nil {
		return e.Logger
	}
	return slog.Default()
}

func (e *Engine) maxPerHour() int {
	if e.MaxPerHour > 0 {
		return e.MaxPerHour
	}
	return 6
}
