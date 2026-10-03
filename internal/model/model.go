// Package model is the shared vocabulary of phonehome. Every other package
// speaks in these types, so this package must stay dependency-free (stdlib
// only) and changes to it are contract changes.
package model

import (
	"net/netip"
	"time"
)

// Category is what a network destination is *for*, in plain terms.
type Category string

const (
	CatACR       Category = "acr"       // automatic content recognition: fingerprints what is on screen
	CatAds       Category = "ads"       // ad serving, ad measurement, ad SDKs
	CatTracking  Category = "tracking"  // cross-device / cross-app profiling, data brokers
	CatTelemetry Category = "telemetry" // vendor analytics, diagnostics, usage logging
	CatEssential Category = "essential" // firmware updates, time sync, connectivity checks, auth
	CatContent   Category = "content"   // the thing you actually asked for: streams, apps, CDNs
	CatUnknown   Category = "unknown"   // not in the knowledge base (yet)
)

// Categories returns every category in display order (worst first).
func Categories() []Category {
	return []Category{CatACR, CatAds, CatTracking, CatTelemetry, CatEssential, CatContent, CatUnknown}
}

// Snooping reports whether traffic in this category is about you rather than for you.
func (c Category) Snooping() bool {
	switch c {
	case CatACR, CatAds, CatTracking, CatTelemetry:
		return true
	}
	return false
}

// Label is the human-readable name used in the UI and on receipts.
func (c Category) Label() string {
	switch c {
	case CatACR:
		return "Content recognition"
	case CatAds:
		return "Advertising"
	case CatTracking:
		return "Tracking"
	case CatTelemetry:
		return "Telemetry"
	case CatEssential:
		return "Essential"
	case CatContent:
		return "Content & apps"
	}
	return "Unknown"
}

// Valid reports whether c is one of the defined categories.
func (c Category) Valid() bool {
	for _, k := range Categories() {
		if c == k {
			return true
		}
	}
	return false
}

// DNSQuery is one DNS lookup made by a device on the local network.
type DNSQuery struct {
	Time     time.Time
	ClientIP netip.Addr
	Domain   string // lowercased, no trailing dot
	QType    string // "A", "AAAA", "HTTPS", ... ("" if unknown)
	Blocked  bool   // the resolver refused/sinkholed it
	Source   string // name of the source that produced it, e.g. "pihole"
}

// Flow is one network connection observed leaving the local network
// (e.g. from conntrack). Bytes may be zero when the source cannot count them.
type Flow struct {
	Start      time.Time
	End        time.Time // zero if still open / unknown
	ClientIP   netip.Addr
	RemoteIP   netip.Addr
	RemotePort uint16
	Proto      string // "tcp" or "udp"
	BytesOut   uint64
	BytesIn    uint64
	Source     string
}

// DeviceKind is a coarse guess at what a device is.
type DeviceKind string

const (
	KindTV        DeviceKind = "tv"
	KindStreamer  DeviceKind = "streamer" // Roku stick, Fire TV, Chromecast, Apple TV
	KindSpeaker   DeviceKind = "speaker"  // smart speakers & voice assistants
	KindCamera    DeviceKind = "camera"   // incl. doorbells
	KindVacuum    DeviceKind = "vacuum"
	KindPlug      DeviceKind = "plug" // plugs, bulbs, switches
	KindHub       DeviceKind = "hub"  // smart-home hubs and bridges
	KindAppliance DeviceKind = "appliance"
	KindConsole   DeviceKind = "console"
	KindPhone     DeviceKind = "phone"
	KindComputer  DeviceKind = "computer"
	KindNetwork   DeviceKind = "network" // routers, APs, NAS
	KindUnknown   DeviceKind = "unknown"
)

// Device is a thing on the local network. ID is stable across IP changes:
// "mac:<lowercase aa:bb:..>" when the MAC is known, else "ip:<addr>".
type Device struct {
	ID        string
	MAC       string // lowercase colon form, "" if unknown
	IPs       []netip.Addr
	Hostname  string
	Vendor    string // from MAC OUI or source metadata
	Kind      DeviceKind
	Label     string // user-assigned friendly name; wins over everything for display
	FirstSeen time.Time
	LastSeen  time.Time
}

// DisplayName is what we call the device in the UI.
func (d Device) DisplayName() string {
	switch {
	case d.Label != "":
		return d.Label
	case d.Hostname != "":
		return d.Hostname
	case d.Vendor != "":
		return d.Vendor + " device"
	case len(d.IPs) > 0:
		return d.IPs[0].String()
	}
	return d.ID
}

// Company is an organisation that operates network destinations.
type Company struct {
	ID      string // short slug, e.g. "samsung"
	Name    string
	Country string // ISO 3166-1 alpha-2 of headquarters, "" if unknown
	URL     string
}

// Classification is what the knowledge base knows about one domain.
type Classification struct {
	Domain     string
	Rule       string // the KB pattern that matched, "" if none
	Category   Category
	Company    *Company // nil if unknown
	Purpose    string   // one plain-English sentence, "" if unknown
	Confidence string   // "high", "medium", "low" ("" if unmatched)
	Evidence   []string // URLs backing the claim
	KindHint   DeviceKind
}

// Fix is a concrete thing the user can do to reduce snooping.
type Fix struct {
	ID       string
	Title    string
	Vendors  []string     // lowercase substrings matched against Device.Vendor/hostname/companies seen
	Kinds    []DeviceKind // empty = any kind
	When     []string     // findings that must be present: a Category ("acr") or "bypass"; empty = always
	Steps    []string
	Notes    string
	Evidence []string
}

// Period is a half-open time range [From, To).
type Period struct {
	From time.Time
	To   time.Time
}

// Days is the length of the period in (fractional) days, minimum 1/24.
func (p Period) Days() float64 {
	d := p.To.Sub(p.From).Hours() / 24
	if d < 1.0/24 {
		return 1.0 / 24
	}
	return d
}

// Previous is the period of the same length that ends where p starts.
func (p Period) Previous() Period {
	return Period{From: p.From.Add(-p.To.Sub(p.From)), To: p.From}
}

// DomainStat is how often a device looked up one domain.
type DomainStat struct {
	Domain      string
	Count       int
	Blocked     int
	Category    Category
	CompanyID   string
	CompanyName string
	Purpose     string
}

// UnknownDomain is a domain a device looked up that the knowledge base does
// not cover yet: the raw material for a new rule.
type UnknownDomain struct {
	Domain string
	Group  string // registrable domain, best effort (no public-suffix list)
	Count  int
	First  time.Time
	Last   time.Time
}

// CompanyStat is how often a device talked to one company.
type CompanyStat struct {
	ID      string
	Name    string
	Country string
	Count   int
}

// Heartbeat is a destination contacted on a regular clock, independent of use.
type Heartbeat struct {
	Domain   string
	Category Category
	Count    int
	Every    time.Duration // median interval between lookups
	Jitter   float64       // coefficient of variation of the intervals (0 = perfectly regular)
}

// Bypass is evidence that a device can or does route around your DNS.
type Bypass struct {
	Kind       string // "doh-lookup", "doh-flow", "dot-flow", "foreign-dns"
	Detail     string // plain English
	Evidence   string // the domain/IP:port that proves it
	Confidence string // "high", "medium", "low"
}

// DeviceReport is everything phonehome has to say about one device in a period.
type DeviceReport struct {
	Device     Device
	Period     Period
	Total      int // DNS lookups
	Blocked    int
	ByCategory map[Category]int
	Snooping   int           // sum of snooping categories
	SnoopShare float64       // Snooping / Total (0 when Total == 0)
	PerDay     float64       // Snooping per day
	Hourly     [24]int       // lookups by local hour of day
	TopDomains []DomainStat  // desc by Count, at most 25
	Companies  []CompanyStat // desc by Count
	Countries  []string      // ISO codes of companies' HQs, desc by traffic
	Heartbeats []Heartbeat
	QuietHours int    // lookups during the quiet-hours window
	QuietLabel string // e.g. "01:00–06:00"
	Bypasses   []Bypass
	Fixes      []Fix
	Grade      string // "A".."F", see docs/grading.md
	Flows      int    // connections observed (0 in DNS-only mode)
	// Unknown lists unclassified domains, desc by Count, at most 50. Local
	// names and reverse lookups are left out: no rule can describe them and
	// they can identify the household.
	Unknown []UnknownDomain
	// Previous compares this device with the period just before Period; nil
	// when the home has no comparison (see HomeReport.Previous).
	Previous *Comparison
}

// Comparison sets a period's figures beside those of the period of equal
// length just before it. The rules (when one is made, what Partial means)
// are in docs/grading.md, "Compared with the previous period".
type Comparison struct {
	Period  Period  // the previous period: same length, ending where this one starts
	Days    float64 // days of the previous period with data; < Period.Days() when Partial
	Partial bool    // stored data begins inside the previous period
	// Seen reports whether the device made lookups or connections in the
	// previous period. Always true for a home. When false, the figures below
	// describing the previous period are zero and Grade is "".
	Seen     bool
	Total    int     // previous lookups
	Snooping int     // previous snooping lookups
	PerDay   float64 // previous snooping lookups per day of data
	// NowPerDay is this period's snooping lookups per day by the same rule
	// (equal to DeviceReport.PerDay for a device), so the two sit side by
	// side.
	NowPerDay  float64
	Grade      string           // previous grade, "" when not Seen
	ByCategory map[Category]int // previous lookups per category
	// CategoryDelta is the change in lookups per day, per category: this
	// period's rate minus the previous one's.
	CategoryDelta map[Category]float64
	// Stopped lists snooping heartbeats detected in the previous period but
	// not in this one; Started the reverse. Devices only, and only when Seen.
	Stopped []Heartbeat
	Started []Heartbeat
}

// Change is the relative change in snooping lookups per day: -0.92 means 92%
// fewer, 0.5 means 50% more. ok is false when there is no meaningful ratio:
// the device was not seen before, or it went from none to some.
func (c Comparison) Change() (change float64, ok bool) {
	switch {
	case !c.Seen:
		return 0, false
	case c.PerDay == 0 && c.NowPerDay == 0:
		return 0, true
	case c.PerDay == 0:
		return 0, false
	}
	return (c.NowPerDay - c.PerDay) / c.PerDay, true
}

// HomeComparison compares a whole home with the previous period.
type HomeComparison struct {
	Comparison
	Devices int // devices active in the previous period
	// Gone holds the previous-period reports of devices that were active
	// then but made no lookups or connections in this period, worst first.
	// It cannot say why: unplugged, switched off, or a new address phonehome
	// cannot tie to the old device.
	Gone []DeviceReport
}

// HomeReport is the whole-network view.
type HomeReport struct {
	Period      Period
	GeneratedAt time.Time
	Devices     []DeviceReport // desc by Snooping
	Total       int
	Snooping    int
	ByCategory  map[Category]int
	Grade       string // "A".."F", the worst device grade; "" with no devices. See docs/grading.md
	Demo        bool   // true when built from synthetic demo data
	// Previous compares the home with the period of equal length just
	// before Period. nil when there is nothing honest to compare with: no
	// stored data then, too little of it, or no lookups at all. See
	// docs/grading.md.
	Previous *HomeComparison
}

// SourceStatus is the health of one ingestion source.
type SourceStatus struct {
	Name      string
	Kind      string // "dns", "flow", "devices"
	LastRun   time.Time
	LastOK    time.Time
	Records   int64 // total records ingested
	LastError string
}

// Status is the health of the whole instance.
type Status struct {
	Version string
	DBPath  string
	Demo    bool
	Sources []SourceStatus
	Devices int
	Oldest  time.Time // oldest DNS record stored
	Newest  time.Time
	Setup   Setup // how sources were chosen; zero for demo data
}

// Setup is how the running instance chose its sources, so first-run
// screens can say what was found and what could not be read.
type Setup struct {
	AutoDetect bool          // no sources configured; phonehome looks for well-known files
	CheckedAt  time.Time     // last auto-detection run
	Sources    []SetupSource // sources in use, configured or detected
	Problems   []SetupProblem
}

// SetupSource is one source phonehome reads.
type SetupSource struct {
	Type     string // config source type, e.g. "pihole-db"
	Location string // file path or URL
}

// SetupProblem is a well-known file auto-detection found but could not use.
type SetupProblem struct {
	Type     string
	Path     string
	Problem  string // e.g. "permission denied"
	Hint     string // how to fix it
	Optional bool   // the source only adds detail; lookups come from elsewhere
}
