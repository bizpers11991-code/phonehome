// Package receipt lays out phonehome's Privacy Receipt, a thermal-paper style
// summary of what a device (or a whole home) sends home, and renders it to SVG
// and PNG. Both renderers draw the same vector outlines of the embedded Go Mono
// font, so the two formats look identical and need no installed fonts.
package receipt

import (
	"fmt"
	"hash"
	"hash/fnv"
	"slices"
	"strings"
	"time"

	"github.com/bizpers11991-code/phonehome/internal/model"
)

// Cols is the width of the receipt grid in monospace cells.
const Cols = 42

// DefaultWidth is 80 mm thermal paper at 203 dpi.
const DefaultWidth = 576

const (
	repo   = "github.com/bizpers11991-code/phonehome"
	marker = "›"
	keyW   = 9 // width of the "PRINTED  " meta-key column
)

// Options controls a receipt.
type Options struct {
	Demo  bool      // stamp a DEMO DATA watermark across the paper
	Now   time.Time // printed time; its location is used for every date (zero: time.Now())
	Width int       // pixels (default DefaultWidth); the layout scales with it
}

// Kind is the kind of a receipt line.
type Kind int

const (
	Text       Kind = iota // Line.Text, at most Cols cells
	Header                 // double-width, double-height, at most Cols/2 cells
	Rule                   // dashed rule
	DoubleRule             // double rule
	Spacer                 // half a line of blank paper
	Stamp                  // rubber-stamped grade (Line.Text) beside Line.Aside
	Barcode                // decorative barcode derived from Doc's content hash
)

// Line is one row of the receipt. Text is final: padded, truncated and
// sanitised to the grid, so renderers just draw it cell by cell.
type Line struct {
	Kind   Kind
	Text   string
	Bold   bool
	Accent bool
	Aside  []string // Stamp only: lines printed to the left of the stamp
}

// Doc is a laid-out receipt, independent of output format.
type Doc struct {
	Title string // short description, used as the SVG title
	Width int
	Demo  bool
	Lines []Line
	seed  uint64 // content hash; drives decorative details (barcode, torn edge, ink)
}

// stampAside is how many cells the stamp leaves for Line.Aside.
const stampAside = 25

type builder struct {
	doc Doc
	h   hash.Hash64
}

func newBuilder(title string, o Options) *builder {
	w := o.Width
	if w <= 0 {
		w = DefaultWidth
	}
	return &builder{doc: Doc{Title: clean(title), Width: w, Demo: o.Demo}, h: fnv.New64a()}
}

func (b *builder) add(l Line) {
	b.doc.Lines = append(b.doc.Lines, l)
	fmt.Fprintf(b.h, "%d|%s|%s\n", l.Kind, l.Text, strings.Join(l.Aside, "|"))
}

func (b *builder) text(s string) { b.add(Line{Kind: Text, Text: fit(s, Cols)}) }
func (b *builder) bold(s string) { b.add(Line{Kind: Text, Text: fit(s, Cols), Bold: true}) }
func (b *builder) warn(s string) {
	b.add(Line{Kind: Text, Text: fit(s, Cols), Bold: true, Accent: true})
}
func (b *builder) centered(s string) { b.text(center(s, Cols)) }
func (b *builder) kind(k Kind)       { b.add(Line{Kind: k}) }

// meta prints a till-style "KEY      value" line, wrapping the value.
func (b *builder) meta(key, val string) {
	for i, l := range wrap(clean(val), Cols-keyW) {
		if i > 0 {
			key = ""
		}
		b.text(fmt.Sprintf("%-*s%s", keyW, key, l))
	}
}

// para wraps s in bold after a hanging prefix, e.g. "FIX: ".
func (b *builder) para(prefix, s string) {
	for i, l := range wrap(clean(s), Cols-width(prefix)) {
		if i > 0 {
			prefix = strings.Repeat(" ", width(prefix))
		}
		b.add(Line{Kind: Text, Text: prefix + l, Bold: true})
	}
}

func (b *builder) masthead(o Options, subtitle string) {
	b.add(Line{Kind: Header, Text: "PHONEHOME", Bold: true})
	b.centered("PRIVACY RECEIPT")
	if subtitle != "" {
		b.bold(center(subtitle, Cols))
	}
	if o.Demo {
		b.kind(Spacer)
		b.warn(center("DEMO DATA · NOT A MEASUREMENT", Cols))
	}
	b.kind(Rule)
}

// categories prints the non-zero categories worst first, calling sub after
// each one for indented detail lines.
func (b *builder) categories(by map[model.Category]int, sub func(model.Category)) {
	b.bold(leader("WHAT WENT OUT", "LOOKUPS", Cols))
	any := false
	for _, c := range model.Categories() {
		if by[c] == 0 {
			continue
		}
		any = true
		b.text(leader(strings.ToUpper(c.Label()), thousands(by[c]), Cols))
		if sub != nil {
			sub(c)
		}
	}
	if !any {
		b.text("  nothing heard in this period")
	}
}

func (b *builder) recipients(cs []model.CompanyStat) {
	if len(cs) == 0 {
		return
	}
	b.kind(Spacer)
	b.bold("TOP RECIPIENTS")
	for _, c := range cs[:min(5, len(cs))] {
		name := clean(c.Name)
		if name == "" {
			name = clean(c.ID)
		}
		cc := ""
		if needsCountry(name, c.Country) {
			cc = " (" + clean(c.Country) + ")"
		}
		val := thousands(c.Count)
		b.text(leader(fit(name, Cols-width(val)-width(cc)-5)+cc, val, Cols))
	}
}

// needsCountry reports whether a " (CC)" suffix would add anything to name:
// not when the country is unknown, the name already mentions "(CC)", or it
// already ends in a parenthesised remark such as "(Germany)".
func needsCountry(name, cc string) bool {
	return cc != "" && !strings.Contains(strings.ToUpper(name), "("+strings.ToUpper(cc)+")") && !strings.HasSuffix(name, ")")
}

func (b *builder) totals(total int, share, perDay float64) {
	b.kind(DoubleRule)
	b.bold(leader("TOTAL CALLS HOME", thousands(total), Cols))
	b.bold(leader("ABOUT YOU (SNOOPING)", percent(share), Cols))
	b.bold(leader("SNOOPING PER DAY", rate(perDay), Cols))
}

func (b *builder) stamp(grade string, aside []string) {
	if grade == "" {
		grade = "?"
	}
	for i := range aside {
		aside[i] = fit(aside[i], stampAside)
	}
	b.add(Line{Kind: Stamp, Text: fit(clean(grade), 1), Bold: true, Accent: true, Aside: aside})
}

func (b *builder) fix(fs []model.Fix) {
	if len(fs) == 0 {
		return
	}
	b.kind(Rule)
	b.para("FIX: ", fs[0].Title)
	if n := len(fs) - 1; n > 0 {
		b.text(fmt.Sprintf("     (+%d more in phonehome)", n))
	}
}

func (b *builder) footer() *Doc {
	b.kind(Rule)
	b.centered("* NOTHING LEFT THIS HOUSE *")
	b.centered("* TO MAKE THIS RECEIPT *")
	b.kind(Spacer)
	b.bold(center("THANK YOU FOR YOUR DATA", Cols))
	b.kind(Spacer)
	b.doc.seed = b.h.Sum64()
	b.kind(Barcode)
	b.centered(serial(b.doc.seed))
	b.centered(repo)
	return &b.doc
}

// serial is a decorative receipt number derived from the content hash.
func serial(seed uint64) string {
	s := fmt.Sprintf("%016X", seed)
	return "NO. " + s[:4] + " " + s[4:8] + " " + s[8:12]
}

var kindLabels = map[model.DeviceKind]string{
	model.KindTV:        "Smart TV",
	model.KindStreamer:  "Streaming device",
	model.KindSpeaker:   "Smart speaker",
	model.KindCamera:    "Camera",
	model.KindVacuum:    "Robot vacuum",
	model.KindPlug:      "Plug / bulb",
	model.KindHub:       "Smart-home hub",
	model.KindAppliance: "Appliance",
	model.KindConsole:   "Game console",
	model.KindPhone:     "Phone",
	model.KindComputer:  "Computer",
	model.KindNetwork:   "Network gear",
}

func kindLabel(d model.Device) string {
	k, ok := kindLabels[d.Kind]
	if !ok {
		k = "Unknown device"
	}
	if d.Vendor != "" {
		k += " · " + d.Vendor
	}
	return k
}

// verdicts are the stamp's headlines for a grade decided by volume alone.
var verdicts = map[string][]string{
	"A": {"MINDS ITS OWN", "BUSINESS."},
	"B": {"MOSTLY MINDS ITS", "OWN BUSINESS."},
	"C": {"CHATTY ABOUT YOU."},
	"D": {"TALKS ABOUT YOU", "A LOT."},
	"F": {"CAN'T STOP TALKING", "ABOUT YOU."},
}

// headline is the stamp's verdict, taken from the rule that decided the
// grade so it is never wrong: a TV graded D for a DNS bypass does not
// "talk about you a lot".
func headline(grade string, why model.GradeReason) []string {
	switch why.Rule {
	case model.ReasonACRHeartbeat:
		return []string{"WATCHES WHAT YOU WATCH,", "ON A CLOCK."}
	case model.ReasonACR:
		return []string{"TALKS TO CONTENT-", "RECOGNITION SERVERS."}
	case model.ReasonBypass:
		return []string{"GOES AROUND", "YOUR DNS FILTER."}
	}
	return verdicts[grade]
}

// why prints, under the stamp, what decided the grade and how far to trust
// it.
func (b *builder) why(r model.DeviceReport) {
	if s := r.Reason.Text(); s != "" {
		b.note("WHY: ", s+".")
	}
	if s := r.Reason.ProvisionalText(); s != "" {
		b.note("", s+".")
	}
	if s := r.CoverageNote(); s != "" {
		b.note("", s+".")
	}
	if r.ACRUnseen() {
		b.note("", model.ACRUnseenNote)
	}
}

// note wraps s after a hanging prefix, like para but not bold.
func (b *builder) note(prefix, s string) {
	for i, l := range wrap(clean(s), Cols-width(prefix)) {
		if i > 0 {
			prefix = strings.Repeat(" ", width(prefix))
		}
		b.text(prefix + l)
	}
}

func bypassLine(bp model.Bypass) string {
	what := "BYPASSES YOUR DNS: "
	if bp.Kind == "doh-lookup" {
		what = "CAN BYPASS YOUR DNS: "
	}
	ev := clean(bp.Evidence)
	if ev == "" {
		ev = clean(bp.Detail)
	}
	return "! " + what + ev
}

func when(o Options) time.Time {
	if o.Now.IsZero() {
		return time.Now()
	}
	return o.Now
}

// Device lays out the receipt for one device.
func Device(r model.DeviceReport, o Options) Doc {
	now := when(o)
	name := r.Device.DisplayName()
	b := newBuilder("phonehome privacy receipt: "+name, o)
	b.masthead(o, "")
	b.meta("DEVICE", name)
	b.meta("KIND", kindLabel(r.Device))
	b.meta("PERIOD", period(r.Period.From, r.Period.To, now.Location()))
	b.meta("PRINTED", now.Format("Jan 2 15:04"))
	b.kind(Rule)

	around := !slices.Contains(r.Hourly[:], 0)
	b.categories(r.ByCategory, func(c model.Category) {
		n := 0
		for _, h := range printable(r.Heartbeats, r.Device) {
			if h.Category != c || n == 2 {
				continue
			}
			n++
			e := every(h.Every)
			if around {
				e += ", 24/7"
			}
			b.text("  " + marker + " " + e + " (heartbeat)")
			b.text("    " + fit(clean(h.Domain), Cols-4))
		}
	})
	if r.QuietHours > 0 || r.Blocked > 0 {
		b.kind(Spacer)
	}
	if r.QuietHours > 0 {
		b.text(leader("WHILE YOU SLEPT "+clean(r.QuietLabel), thousands(r.QuietHours), Cols))
	}
	if r.Blocked > 0 {
		b.text(leader("BLOCKED BY YOUR FILTER", thousands(r.Blocked), Cols))
	}
	b.recipients(r.Companies)

	if len(r.Bypasses) > 0 {
		b.kind(Spacer)
		seen := map[string]bool{}
		for _, bp := range r.Bypasses {
			l := bypassLine(bp)
			if seen[l] || len(seen) == 3 {
				continue
			}
			seen[l] = true
			for _, w := range wrap(l, Cols-2) {
				if !strings.HasPrefix(w, "!") {
					w = "  " + w
				}
				b.warn(w)
			}
		}
	}

	b.totals(r.Total, r.SnoopShare, r.PerDay)
	if r.Previous != nil {
		b.since(r.Previous, "GRADE", r.Grade, printable(r.Previous.Stopped, r.Device))
	}
	b.kind(Rule)
	aside := []string{"PRIVACY GRADE", "A QUIET ... F LOUD", ""}
	b.stamp(r.Grade, append(aside, headline(r.Grade, r.Reason)...))
	b.why(r)
	b.fix(r.Fixes)
	return *b.footer()
}

// worse reports whether grade a is worse than grade b ("F" is worst).
func worse(a, b string) bool { return a > b }

// Home lays out the whole-home receipt.
func Home(r model.HomeReport, o Options) Doc {
	o.Demo = o.Demo || r.Demo
	now := when(o)
	b := newBuilder("phonehome privacy receipt: your home", o)
	b.masthead(o, "YOUR HOME")
	b.meta("DEVICES", thousands(len(r.Devices)))
	b.meta("PERIOD", period(r.Period.From, r.Period.To, now.Location()))
	b.meta("PRINTED", now.Format("Jan 2 15:04"))
	b.kind(Rule)

	const maxDevices = 20
	b.bold(leader("DEVICE", "CALLS  GRADE", Cols))
	var worst *model.DeviceReport
	for i := range r.Devices {
		d := &r.Devices[i]
		if worst == nil || worse(d.Grade, worst.Grade) {
			worst = d
		}
		if i == maxDevices {
			b.text(fmt.Sprintf("  + %d more devices", len(r.Devices)-maxDevices))
			continue
		}
		if i > maxDevices {
			continue
		}
		grade := fit(clean(d.Grade), 1)
		if grade == "" {
			grade = "?"
		}
		b.text(leader(clean(d.Device.DisplayName()), thousands(d.Total)+"    "+grade+"  ", Cols))
	}
	b.kind(Spacer)
	b.categories(r.ByCategory, nil)

	var cs []model.CompanyStat
	idx := map[string]int{}
	var bypass []string
	for _, d := range r.Devices {
		for _, c := range d.Companies {
			if i, ok := idx[c.ID]; ok {
				cs[i].Count += c.Count
				continue
			}
			idx[c.ID] = len(cs)
			cs = append(cs, c)
		}
		if len(d.Bypasses) > 0 {
			bypass = append(bypass, clean(d.Device.DisplayName()))
		}
	}
	slices.SortStableFunc(cs, func(a, b model.CompanyStat) int { return b.Count - a.Count })
	b.recipients(cs)

	if len(bypass) > 0 {
		b.kind(Spacer)
		b.warn("! CAN BYPASS YOUR DNS:")
		for i, n := range bypass {
			if i == 3 {
				b.warn(fmt.Sprintf("  + %d more", len(bypass)-3))
				break
			}
			b.warn("  " + fit(n, Cols-2))
		}
	}

	var share, perDay float64
	if r.Total > 0 {
		share = float64(r.Snooping) / float64(r.Total)
	}
	perDay = float64(r.Snooping) / r.Period.Days()
	if r.Previous != nil { // per day of data, so it matches the comparison below
		perDay = r.Previous.NowPerDay
	}
	b.totals(r.Total, share, perDay)

	if worst != nil {
		g := fit(clean(worst.Grade), 1)
		val := " (" + g + ")"
		b.bold("WORST OFFENDER  " + fit(clean(worst.Device.DisplayName()), Cols-16-width(val)) + val)
	}
	if r.Previous != nil {
		b.since(&r.Previous.Comparison, "HOME GRADE", r.Grade, homeStopped(r))
	}
	b.kind(Rule)
	// The home grade is computed by analyze (HomeReport.Grade); the receipt
	// only prints it.
	b.stamp(r.Grade, []string{"HOME GRADE", "A QUIET ... F LOUD", "", "AS GOOD AS ITS", "WORST DEVICE."})
	if worst != nil {
		b.fix(worst.Fixes)
	}
	return *b.footer()
}
