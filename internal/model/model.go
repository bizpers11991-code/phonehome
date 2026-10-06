// Package model is the shared vocabulary of phonehome. Every other package
// speaks in these types, so this package must stay dependency-free (stdlib
// only) and changes to it are contract changes.
package model

import (
	"fmt"
	"math"
	"net/netip"
	"strconv"
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
	Rule        string   // the KB pattern that matched, "" if none
	Confidence  string   // the rule's confidence, "" if unmatched
	Evidence    []string // the rule's evidence URLs
	First, Last time.Time
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
	Domains    []DomainStat  // every domain, desc by Count; TopDomains is its start
	Companies  []CompanyStat // desc by Count
	Countries  []string      // ISO codes of companies' HQs, desc by traffic
	Heartbeats []Heartbeat
	QuietHours int    // lookups during the quiet-hours window
	QuietLabel string // e.g. "01:00–06:00"
	Bypasses   []Bypass
	Fixes      []Fix
	Grade      string      // "A".."F", see docs/grading.md
	Reason     GradeReason // the rule that decided Grade, with its numbers
	Flows      int         // connections observed (0 in DNS-only mode)
	// Unknown lists unclassified domains, desc by Count, at most 50. Local
	// names and reverse lookups are left out: no rule can describe them and
	// they can identify the household.
	Unknown []UnknownDomain
	// Previous compares this device with the period just before Period; nil
	// when the home has no comparison (see HomeReport.Previous).
	Previous *Comparison
}

// Coverage is the share of lookups the knowledge base classified, from 0
// to 1 (0 when there are none). Unclassified lookups never count against a
// device, so a grade says less the lower its coverage.
func (r DeviceReport) Coverage() float64 {
	if r.Total <= 0 {
		return 0
	}
	known := r.Total - r.ByCategory[CatUnknown]
	return min(max(float64(known)/float64(r.Total), 0), 1)
}

// LowCoverage is the coverage under which a grade is flagged as resting on
// a minority of the device's lookups.
const LowCoverage = 0.5

// CoverageLow reports whether r's grade rests on less than LowCoverage of
// its lookups.
func (r DeviceReport) CoverageLow() bool {
	return r.Total > 0 && r.Coverage() < LowCoverage
}

// CoveragePercent is Coverage as a whole percentage, rounded down so that
// 49.6% never reads as the 50% it falls short of. It is worked out in
// integers: in floating point 29 of 100 would come to 28.999…%.
func (r DeviceReport) CoveragePercent() int {
	if r.Total <= 0 {
		return 0
	}
	known := min(max(r.Total-r.ByCategory[CatUnknown], 0), r.Total)
	return known * 100 / r.Total
}

// CoverageNote is "Graded on the 40% of lookups phonehome recognises" when
// coverage is low, else "".
func (r DeviceReport) CoverageNote() string {
	if !r.CoverageLow() {
		return ""
	}
	return fmt.Sprintf("Graded on the %d%% of lookups phonehome recognises", r.CoveragePercent())
}

// ACRUnseenNote is printed for a TV or streaming player when ACRUnseen.
const ACRUnseenNote = "No known content-recognition server seen; not every TV brand's servers are known."

// ACRUnseen reports whether r is a TV or streaming player that looked up no
// known content-recognition server. That is no proof it has no ACR: not
// every platform's ACR servers are in the knowledge base.
func (r DeviceReport) ACRUnseen() bool {
	return (r.Device.Kind == KindTV || r.Device.Kind == KindStreamer) && r.ByCategory[CatACR] == 0
}

// Grade reason rules, in GradeReason.Rule. See docs/grading.md.
const (
	ReasonVolume       = "volume"        // snooping lookups per day decided the grade
	ReasonACRHeartbeat = "acr-heartbeat" // content recognition on a clock: F
	ReasonACR          = "acr"           // any content-recognition lookup: at least D
	ReasonBypass       = "bypass"        // a high-confidence DNS bypass: at least D
)

// GradeReason says why a device got its grade: the rule that decided it and
// the numbers that triggered it, so every grade can explain itself.
type GradeReason struct {
	Rule   string  // one of the Reason constants; "" when not graded
	PerDay float64 // snooping lookups per day
	// VolumeGrade is the grade PerDay alone earns, and [BandFrom, BandTo)
	// its range of lookups per day (BandTo 0: no upper limit). It differs
	// from the device's grade when content recognition or a bypass raised it.
	VolumeGrade      string
	BandFrom, BandTo int
	Count            int           // ReasonACR: content-recognition lookups
	Evidence         string        // ReasonACRHeartbeat: the domain; ReasonBypass: the server, e.g. "8.8.8.8:443"
	Every            time.Duration // ReasonACRHeartbeat: the heartbeat's interval
	// Data is how much of the period has data for the device. Provisional
	// is true when that is too little for a rate per day to mean much: the
	// grade is still shown, marked provisional, and alerts ignore it.
	Data        time.Duration
	Provisional bool
}

// Text is the reason as one short English line, e.g. "1,359 snooping
// lookups a day (C is 300–1,499)". The dashboard words it in its own
// languages from the same fields.
func (g GradeReason) Text() string {
	return g.TextRate(thousands(g.Lookups()))
}

// TextRate is Text with the lookups per day already formatted, for a
// surface that prints small rates to a decimal place ("3.6").
func (g GradeReason) TextRate(perDay string) string {
	lookups := "lookups"
	if perDay == "1" {
		lookups = "lookup"
	}
	switch g.Rule {
	case ReasonACRHeartbeat:
		return "Content recognition (ACR) on a clock, " + everyText(g.Every) + ": always F"
	case ReasonACR:
		return fmt.Sprintf("Contacts content-recognition (ACR) servers (%s %s): at least D",
			thousands(g.Count), plural(g.Count, "lookup", "lookups"))
	case ReasonBypass:
		return fmt.Sprintf("Bypasses your DNS via %s: at least D (its %s snooping %s a day alone would be %s)",
			g.Evidence, perDay, lookups, g.VolumeGrade)
	case ReasonVolume:
		return fmt.Sprintf("%s snooping %s a day (%s)", perDay, lookups, g.Band())
	}
	return ""
}

// Lookups is PerDay as a whole number for print: rounded, but kept inside
// VolumeGrade's band so that 49.6 a day reads 49, not the 50 that
// "A is under 50" rules out.
func (g GradeReason) Lookups() int {
	n := int(math.Round(g.PerDay))
	if g.BandTo > 0 {
		n = min(n, g.BandTo-1)
	}
	return max(n, g.BandFrom)
}

// Band describes VolumeGrade's range, e.g. "C is 300–1,499".
func (g GradeReason) Band() string {
	switch {
	case g.VolumeGrade == "":
		return ""
	case g.BandFrom == 0:
		return fmt.Sprintf("%s is under %s", g.VolumeGrade, thousands(g.BandTo))
	case g.BandTo == 0:
		return fmt.Sprintf("%s is %s or more", g.VolumeGrade, thousands(g.BandFrom))
	}
	return fmt.Sprintf("%s is %s–%s", g.VolumeGrade, thousands(g.BandFrom), thousands(g.BandTo-1))
}

// ProvisionalText is "Provisional: based on 3 hours of data", or "" when
// the grade is not provisional.
func (g GradeReason) ProvisionalText() string {
	if !g.Provisional {
		return ""
	}
	if h := int(g.Data.Hours()); h >= 1 {
		return fmt.Sprintf("Provisional: based on %d %s of data", h, plural(h, "hour", "hours"))
	}
	return "Provisional: based on less than an hour of data"
}

// everyText words a heartbeat's interval as the Privacy Receipt's heartbeat
// lines do ("every 15s", "every 5 min"), so the reason printed under them
// matches.
func everyText(d time.Duration) string {
	switch s := d.Seconds(); {
	case d <= 0:
		return "regularly"
	case s < 59.5:
		return fmt.Sprintf("every %ds", max(1, int(math.Round(s))))
	case s < 90*60:
		return fmt.Sprintf("every %d min", int(math.Round(s/60)))
	case s < 36*3600:
		return fmt.Sprintf("every %d h", int(math.Round(s/3600)))
	}
	return fmt.Sprintf("every %d days", int(math.Round(d.Hours()/24)))
}

func plural(n int, one, other string) string {
	if n == 1 {
		return one
	}
	return other
}

// thousands formats a non-negative n with comma separators: 12345 → "12,345".
func thousands(n int) string {
	s := strconv.Itoa(n)
	for i := len(s) - 3; i > 0; i -= 3 {
		s = s[:i] + "," + s[i:]
	}
	return s
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
