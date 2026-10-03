package web

import (
	"strconv"
	"strings"
	"time"

	"github.com/bizpers11991-code/phonehome/internal/model"
)

// The API speaks in these DTOs rather than model types so that the JSON
// stays camelCase and stable while Go structs evolve. Durations are seconds
// and times are RFC 3339 (omitted when unknown).

type reportDTO struct {
	From        string        `json:"from"`
	To          string        `json:"to"`
	Days        float64       `json:"days"`
	GeneratedAt string        `json:"generatedAt,omitempty"`
	Demo        bool          `json:"demo"`
	Total       int           `json:"total"`
	Snooping    int           `json:"snooping"`
	SnoopShare  float64       `json:"snoopShare"`
	Grade       string        `json:"grade"` // home grade: the worst device's; "" with no devices
	Categories  []categoryDTO `json:"categories"`
	Devices     []deviceDTO   `json:"devices"`
	// Previous compares the home with the period of equal length before
	// this one; omitted when there is nothing honest to compare with.
	Previous *homeCompareDTO `json:"previous,omitempty"`
}

// compareDTO sets a period beside the one before it (model.Comparison).
// Change is the relative change in snooping per day (-0.92 = 92% fewer),
// null when there is no meaningful ratio (not seen before, or up from none).
type compareDTO struct {
	From       string           `json:"from"`
	To         string           `json:"to"`
	Days       float64          `json:"days"`     // length of the previous period
	DataDays   float64          `json:"dataDays"` // days of it with data
	Partial    bool             `json:"partial"`
	Seen       bool             `json:"seen"`
	Total      int              `json:"total"`
	Snooping   int              `json:"snooping"`
	PerDay     float64          `json:"perDay"`
	NowPerDay  float64          `json:"nowPerDay"`
	Change     *float64         `json:"change"`
	Grade      string           `json:"grade"`
	Categories []categoryChange `json:"categories"`
	Stopped    []heartbeatDTO   `json:"stopped"`
	Started    []heartbeatDTO   `json:"started"`
}

// categoryChange is one category before and the change in lookups per day.
type categoryChange struct {
	ID          model.Category `json:"id"`
	Label       string         `json:"label"`
	Snooping    bool           `json:"snooping"`
	Before      int            `json:"before"`
	DeltaPerDay float64        `json:"deltaPerDay"`
}

type homeCompareDTO struct {
	compareDTO
	Devices int       `json:"devices"` // active in the previous period
	Gone    []goneDTO `json:"gone"`
}

// goneDTO is a device active in the previous period but not in this one.
type goneDTO struct {
	ID     string  `json:"id"`
	Name   string  `json:"name"`
	Grade  string  `json:"grade"`
	PerDay float64 `json:"perDay"`
}

// categoryDTO describes a category and, where it appears, how many lookups
// fell into it. The report's list includes every category in display order
// so the frontend never needs its own copy of labels or the snooping rule.
type categoryDTO struct {
	ID       model.Category `json:"id"`
	Label    string         `json:"label"`
	Snooping bool           `json:"snooping"`
	Count    int            `json:"count"`
}

type deviceDTO struct {
	ID          string           `json:"id"`
	Name        string           `json:"name"`
	DefaultName string           `json:"defaultName"` // the name shown when the label is cleared
	Label       string           `json:"label"`
	Hostname    string           `json:"hostname"`
	Vendor      string           `json:"vendor"`
	Kind        model.DeviceKind `json:"kind"`
	MAC         string           `json:"mac"`
	PrivateMAC  bool             `json:"privateMac"` // locally administered (randomised) address
	IPs         []string         `json:"ips"`
	FirstSeen   string           `json:"firstSeen,omitempty"`
	LastSeen    string           `json:"lastSeen,omitempty"`
	Total       int              `json:"total"`
	Blocked     int              `json:"blocked"`
	Snooping    int              `json:"snooping"`
	SnoopShare  float64          `json:"snoopShare"`
	PerDay      float64          `json:"perDay"`
	Grade       string           `json:"grade"`
	Flows       int              `json:"flows"`
	Hourly      [24]int          `json:"hourly"`
	Quiet       quietDTO         `json:"quiet"`
	Categories  []categoryDTO    `json:"categories"`
	TopDomains  []domainDTO      `json:"topDomains"`
	Companies   []companyDTO     `json:"companies"`
	Heartbeats  []heartbeatDTO   `json:"heartbeats"`
	Bypasses    []bypassDTO      `json:"bypasses"`
	Fixes       []fixDTO         `json:"fixes"`
	Unknown     []unknownDTO     `json:"unknown"`
	// SuggestURL opens a prefilled GitHub issue for the unknown domains;
	// empty for demo data or when nothing can be shared. See suggestURL.
	SuggestURL string `json:"suggestUrl,omitempty"`
	// Previous compares the device with the period before; omitted when
	// the home has no comparison.
	Previous *compareDTO `json:"previous,omitempty"`
}

// quietDTO is the quiet-hours window; StartHour/EndHour are local hours
// [start, end) and are omitted when the label cannot be parsed.
type quietDTO struct {
	Label     string `json:"label"`
	Lookups   int    `json:"lookups"`
	StartHour *int   `json:"startHour,omitempty"`
	EndHour   *int   `json:"endHour,omitempty"`
}

type domainDTO struct {
	Domain        string         `json:"domain"`
	Count         int            `json:"count"`
	Blocked       int            `json:"blocked"`
	Category      model.Category `json:"category"`
	CategoryLabel string         `json:"categoryLabel"`
	Snooping      bool           `json:"snooping"`
	CompanyID     string         `json:"companyId"`
	CompanyName   string         `json:"companyName"`
	Purpose       string         `json:"purpose"`
}

func newDomain(s model.DomainStat) domainDTO {
	return domainDTO{
		Domain: s.Domain, Count: s.Count, Blocked: s.Blocked,
		Category: s.Category, CategoryLabel: s.Category.Label(), Snooping: s.Category.Snooping(),
		CompanyID: s.CompanyID, CompanyName: s.CompanyName, Purpose: s.Purpose,
	}
}

// domainsDTO is GET /api/devices/{id}/domains: every domain a device looked
// up in the period, with the rule that classified it.
type domainsDTO struct {
	ID      string          `json:"id"`
	Name    string          `json:"name"`
	From    string          `json:"from"`
	To      string          `json:"to"`
	Demo    bool            `json:"demo"`
	Domains []fullDomainDTO `json:"domains"`
}

type fullDomainDTO struct {
	domainDTO
	FirstSeen  string   `json:"firstSeen,omitempty"`
	LastSeen   string   `json:"lastSeen,omitempty"`
	Rule       string   `json:"rule"`       // the knowledge-base pattern, "" when unclassified
	Confidence string   `json:"confidence"` // high, medium, low; "" when unclassified
	Evidence   []string `json:"evidence"`
}

func newDomainsDTO(r model.HomeReport, d model.DeviceReport) domainsDTO {
	out := domainsDTO{ID: d.Device.ID, Name: d.Device.DisplayName(), From: stamp(r.Period.From), To: stamp(r.Period.To),
		Demo: r.Demo, Domains: []fullDomainDTO{}}
	for _, s := range d.Domains {
		ev := s.Evidence
		if ev == nil {
			ev = []string{}
		}
		out.Domains = append(out.Domains, fullDomainDTO{domainDTO: newDomain(s), FirstSeen: stamp(s.First), LastSeen: stamp(s.Last),
			Rule: s.Rule, Confidence: s.Confidence, Evidence: ev})
	}
	return out
}

// unknownDTO is a domain the knowledge base does not cover yet.
type unknownDTO struct {
	Domain    string `json:"domain"`
	Group     string `json:"group"` // registrable domain, best effort
	Count     int    `json:"count"`
	FirstSeen string `json:"firstSeen,omitempty"`
	LastSeen  string `json:"lastSeen,omitempty"`
}

type companyDTO struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	Country string `json:"country"`
	Count   int    `json:"count"`
}

type heartbeatDTO struct {
	Domain        string         `json:"domain"`
	Category      model.Category `json:"category"`
	CategoryLabel string         `json:"categoryLabel"`
	Snooping      bool           `json:"snooping"`
	Count         int            `json:"count"`
	EverySeconds  float64        `json:"everySeconds"`
	Jitter        float64        `json:"jitter"`
}

type bypassDTO struct {
	Kind       string `json:"kind"`
	Detail     string `json:"detail"`
	Evidence   string `json:"evidence"`
	Confidence string `json:"confidence"`
}

type fixDTO struct {
	ID       string   `json:"id"`
	Title    string   `json:"title"`
	Steps    []string `json:"steps"`
	Notes    string   `json:"notes"`
	Evidence []string `json:"evidence"`
}

type statusDTO struct {
	Version string      `json:"version"`
	Demo    bool        `json:"demo"`
	Health  string      `json:"health"` // worst of the sources: "ok", "stale" or "error"
	Devices int         `json:"devices"`
	Oldest  string      `json:"oldest,omitempty"`
	Newest  string      `json:"newest,omitempty"`
	Sources []sourceDTO `json:"sources"`
	Setup   setupDTO    `json:"setup"`
}

// setupDTO tells the first-run screen which sources are in use and which
// well-known files auto-detection found but could not read.
type setupDTO struct {
	AutoDetect bool              `json:"autoDetect"`
	CheckedAt  string            `json:"checkedAt,omitempty"`
	Sources    []setupSourceDTO  `json:"sources"`
	Problems   []setupProblemDTO `json:"problems"`
}

type setupSourceDTO struct {
	Type     string `json:"type"`
	Location string `json:"location"`
}

type setupProblemDTO struct {
	Type     string `json:"type"`
	Path     string `json:"path"`
	Problem  string `json:"problem"`
	Hint     string `json:"hint"`
	Optional bool   `json:"optional"`
}

type sourceDTO struct {
	Name      string `json:"name"`
	Kind      string `json:"kind"`
	Health    string `json:"health"`
	LastRun   string `json:"lastRun,omitempty"`
	LastOK    string `json:"lastOk,omitempty"`
	Records   int64  `json:"records"`
	LastError string `json:"lastError,omitempty"`
}

// staleAfter is how long a source may go without a successful run before
// the footer turns amber.
const staleAfter = 30 * time.Minute

func stamp(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.Format(time.RFC3339)
}

func share(part, total int) float64 {
	if total == 0 {
		return 0
	}
	return float64(part) / float64(total)
}

func newCategory(c model.Category, n int) categoryDTO {
	return categoryDTO{ID: c, Label: c.Label(), Snooping: c.Snooping(), Count: n}
}

func newReportDTO(r model.HomeReport) reportDTO {
	out := reportDTO{
		From:        stamp(r.Period.From),
		To:          stamp(r.Period.To),
		Days:        r.Period.Days(),
		GeneratedAt: stamp(r.GeneratedAt),
		Demo:        r.Demo,
		Total:       r.Total,
		Snooping:    r.Snooping,
		SnoopShare:  share(r.Snooping, r.Total),
		Grade:       r.Grade,
		Devices:     make([]deviceDTO, 0, len(r.Devices)),
	}
	for _, c := range model.Categories() {
		out.Categories = append(out.Categories, newCategory(c, r.ByCategory[c]))
	}
	for _, d := range r.Devices {
		dd := newDeviceDTO(d)
		if !r.Demo { // synthetic domains must not become real issues
			dd.SuggestURL = suggestURL(d)
		}
		out.Devices = append(out.Devices, dd)
	}
	if pc := r.Previous; pc != nil {
		h := &homeCompareDTO{compareDTO: newCompareDTO(pc.Comparison), Devices: pc.Devices, Gone: make([]goneDTO, 0, len(pc.Gone))}
		for _, g := range pc.Gone {
			h.Gone = append(h.Gone, goneDTO{ID: g.Device.ID, Name: g.Device.DisplayName(), Grade: g.Grade, PerDay: g.PerDay})
		}
		out.Previous = h
	}
	return out
}

func newCompareDTO(c model.Comparison) compareDTO {
	out := compareDTO{
		From: stamp(c.Period.From), To: stamp(c.Period.To), Days: c.Period.Days(),
		DataDays: c.Days, Partial: c.Partial, Seen: c.Seen,
		Total: c.Total, Snooping: c.Snooping, PerDay: c.PerDay, NowPerDay: c.NowPerDay,
		Grade:      c.Grade,
		Categories: []categoryChange{},
		Stopped:    newHeartbeats(c.Stopped),
		Started:    newHeartbeats(c.Started),
	}
	if ch, ok := c.Change(); ok {
		out.Change = &ch
	}
	for _, cat := range model.Categories() {
		before, delta := c.ByCategory[cat], c.CategoryDelta[cat]
		if before == 0 && delta == 0 {
			continue
		}
		out.Categories = append(out.Categories, categoryChange{
			ID: cat, Label: cat.Label(), Snooping: cat.Snooping(), Before: before, DeltaPerDay: delta,
		})
	}
	return out
}

func newHeartbeats(hs []model.Heartbeat) []heartbeatDTO {
	out := make([]heartbeatDTO, 0, len(hs))
	for _, h := range hs {
		out = append(out, heartbeatDTO{
			Domain: h.Domain, Category: h.Category, CategoryLabel: h.Category.Label(), Snooping: h.Category.Snooping(),
			Count: h.Count, EverySeconds: h.Every.Seconds(), Jitter: h.Jitter,
		})
	}
	return out
}

func newDeviceDTO(r model.DeviceReport) deviceDTO {
	d := r.Device
	unlabelled := d
	unlabelled.Label = ""
	out := deviceDTO{
		ID:          d.ID,
		Name:        d.DisplayName(),
		DefaultName: unlabelled.DisplayName(),
		Label:       d.Label,
		Hostname:    d.Hostname,
		Vendor:      d.Vendor,
		Kind:        d.Kind,
		MAC:         d.MAC,
		PrivateMAC:  privateMAC(d.MAC),
		IPs:         make([]string, 0, len(d.IPs)),
		FirstSeen:   stamp(d.FirstSeen),
		LastSeen:    stamp(d.LastSeen),
		Total:       r.Total,
		Blocked:     r.Blocked,
		Snooping:    r.Snooping,
		SnoopShare:  r.SnoopShare,
		PerDay:      r.PerDay,
		Grade:       r.Grade,
		Flows:       r.Flows,
		Hourly:      r.Hourly,
		Quiet:       newQuiet(r.QuietLabel, r.QuietHours),
		Categories:  []categoryDTO{},
		TopDomains:  make([]domainDTO, 0, len(r.TopDomains)),
		Companies:   make([]companyDTO, 0, len(r.Companies)),
		Heartbeats:  make([]heartbeatDTO, 0, len(r.Heartbeats)),
		Bypasses:    make([]bypassDTO, 0, len(r.Bypasses)),
		Fixes:       make([]fixDTO, 0, len(r.Fixes)),
		Unknown:     make([]unknownDTO, 0, len(r.Unknown)),
	}
	if out.Kind == "" {
		out.Kind = model.KindUnknown
	}
	for _, ip := range d.IPs {
		out.IPs = append(out.IPs, ip.String())
	}
	for _, c := range model.Categories() {
		if n := r.ByCategory[c]; n > 0 {
			out.Categories = append(out.Categories, newCategory(c, n))
		}
	}
	for _, s := range r.TopDomains {
		out.TopDomains = append(out.TopDomains, newDomain(s))
	}
	for _, u := range r.Unknown {
		out.Unknown = append(out.Unknown, unknownDTO{
			Domain: u.Domain, Group: u.Group, Count: u.Count, FirstSeen: stamp(u.First), LastSeen: stamp(u.Last),
		})
	}
	for _, c := range r.Companies {
		out.Companies = append(out.Companies, companyDTO{ID: c.ID, Name: c.Name, Country: c.Country, Count: c.Count})
	}
	out.Heartbeats = newHeartbeats(r.Heartbeats)
	if r.Previous != nil {
		c := newCompareDTO(*r.Previous)
		out.Previous = &c
	}
	for _, b := range r.Bypasses {
		out.Bypasses = append(out.Bypasses, bypassDTO(b))
	}
	for _, f := range r.Fixes {
		out.Fixes = append(out.Fixes, fixDTO{
			ID: f.ID, Title: f.Title, Steps: nonNil(f.Steps), Notes: f.Notes, Evidence: nonNil(f.Evidence),
		})
	}
	return out
}

// newQuiet parses labels like "01:00–06:00" (en dash or hyphen).
func newQuiet(label string, lookups int) quietDTO {
	q := quietDTO{Label: label, Lookups: lookups}
	from, to, ok := strings.Cut(label, "–")
	if !ok {
		from, to, ok = strings.Cut(label, "-")
	}
	start, ok1 := hourOf(from)
	end, ok2 := hourOf(to)
	if ok && ok1 && ok2 {
		q.StartHour, q.EndHour = &start, &end
	}
	return q
}

func hourOf(s string) (int, bool) {
	h, _, _ := strings.Cut(strings.TrimSpace(s), ":")
	n, err := strconv.Atoi(h)
	return n, err == nil && n >= 0 && n <= 24
}

// privateMAC reports whether mac has the locally-administered bit set, as
// phones do when they use a random address per network.
func privateMAC(mac string) bool {
	if len(mac) < 2 {
		return false
	}
	b, err := strconv.ParseUint(mac[:2], 16, 8)
	return err == nil && b&0x02 != 0
}

func nonNil(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}

func newStatusDTO(s model.Status, now time.Time) statusDTO {
	out := statusDTO{
		Version: s.Version,
		Demo:    s.Demo,
		Health:  "ok",
		Devices: s.Devices,
		Oldest:  stamp(s.Oldest),
		Newest:  stamp(s.Newest),
		Sources: make([]sourceDTO, 0, len(s.Sources)),
		Setup:   newSetupDTO(s.Setup),
	}
	rank := map[string]int{"ok": 0, "stale": 1, "error": 2}
	for _, src := range s.Sources {
		h := sourceHealth(src, now)
		if rank[h] > rank[out.Health] {
			out.Health = h
		}
		out.Sources = append(out.Sources, sourceDTO{
			Name: src.Name, Kind: src.Kind, Health: h,
			LastRun: stamp(src.LastRun), LastOK: stamp(src.LastOK),
			Records: src.Records, LastError: src.LastError,
		})
	}
	if len(s.Sources) == 0 && !s.Demo {
		out.Health = "stale"
	}
	return out
}

func newSetupDTO(s model.Setup) setupDTO {
	out := setupDTO{
		AutoDetect: s.AutoDetect,
		CheckedAt:  stamp(s.CheckedAt),
		Sources:    make([]setupSourceDTO, 0, len(s.Sources)),
		Problems:   make([]setupProblemDTO, 0, len(s.Problems)),
	}
	for _, src := range s.Sources {
		out.Sources = append(out.Sources, setupSourceDTO{Type: src.Type, Location: src.Location})
	}
	for _, p := range s.Problems {
		out.Problems = append(out.Problems, setupProblemDTO{
			Type: p.Type, Path: p.Path, Problem: p.Problem, Hint: p.Hint, Optional: p.Optional,
		})
	}
	return out
}

func sourceHealth(s model.SourceStatus, now time.Time) string {
	switch {
	case s.LastError != "" && s.LastOK.Before(s.LastRun):
		return "error"
	case s.LastOK.IsZero() || now.Sub(s.LastOK) > staleAfter:
		return "stale"
	}
	return "ok"
}
