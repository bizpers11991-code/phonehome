package receipt

import (
	"strings"
	"testing"
	"time"

	"github.com/bizpers11991-code/phonehome/internal/model"
)

func TestThousands(t *testing.T) {
	for n, want := range map[int]string{0: "0", 7: "7", 999: "999", 1000: "1,000", 14302: "14,302", 1234567: "1,234,567", -4200: "-4,200"} {
		if got := thousands(n); got != want {
			t.Errorf("thousands(%d) = %q, want %q", n, got, want)
		}
	}
}

func TestRateAndPercent(t *testing.T) {
	for f, want := range map[float64]string{3.6: "3.6", 4: "4", 1402.4: "1,402", 0: "0"} {
		if got := rate(f); got != want {
			t.Errorf("rate(%v) = %q, want %q", f, got, want)
		}
	}
	if got := percent(0.714); got != "71%" {
		t.Errorf("percent = %q", got)
	}
}

func TestEvery(t *testing.T) {
	for d, want := range map[time.Duration]string{
		200 * time.Millisecond:         "every 1s",
		15 * time.Second:               "every 15s",
		4*time.Minute + 10*time.Second: "every 4 min",
		45 * time.Minute:               "every 45 min",
		2 * time.Hour:                  "every 2 h",
		72 * time.Hour:                 "every 3 days",
	} {
		if got := every(d); got != want {
			t.Errorf("every(%v) = %q, want %q", d, got, want)
		}
	}
}

func TestPeriod(t *testing.T) {
	utc := time.UTC
	day := func(y int, m time.Month, d int) time.Time { return time.Date(y, m, d, 0, 0, 0, 0, utc) }
	for _, c := range []struct {
		from, to time.Time
		want     string
	}{
		{day(2026, 9, 25), day(2026, 10, 2), "Sep 25 – Oct 1, 2026"}, // half-open
		{day(2026, 9, 25), day(2026, 10, 2).Add(14 * time.Hour), "Sep 25 – Oct 2, 2026"},
		{day(2025, 12, 28), day(2026, 1, 4), "Dec 28, 2025 – Jan 3, 2026"},
		{day(2026, 10, 2), day(2026, 10, 3), "Oct 2, 2026"},
	} {
		if got := period(c.from, c.to, utc); got != c.want {
			t.Errorf("period(%v, %v) = %q, want %q", c.from, c.to, got, c.want)
		}
	}
	// Dates follow the location of Options.Now.
	tokyo := time.FixedZone("JST", 9*3600)
	if got := period(day(2026, 9, 24).Add(20*time.Hour), day(2026, 10, 1).Add(20*time.Hour), tokyo); got != "Sep 25 – Oct 2, 2026" {
		t.Errorf("period in JST = %q", got)
	}
}

func TestFitWrapLeader(t *testing.T) {
	if got := fit("Living Room TV", 8); got != "Living…" {
		t.Errorf("fit = %q", got)
	}
	if got := fit("short", 8); got != "short" {
		t.Errorf("fit = %q", got)
	}
	for _, l := range wrap("Turn off Viewing Information Services (Samsung's ACR) supercalifragilisticexpialidocious-and-then-some", 20) {
		if width(l) > 20 {
			t.Errorf("wrapped line %q is wider than 20", l)
		}
	}
	if got := leader("CONTENT RECOGNITION", "9,812", 30); got != "CONTENT RECOGNITION ...  9,812" {
		t.Errorf("leader = %q", got)
	}
	if got := leader(strings.Repeat("X", 60), "1,234", Cols); width(got) != Cols || !strings.Contains(got, "… ..  1,234") {
		t.Errorf("long leader = %q", got)
	}
}

func TestClean(t *testing.T) {
	if got := clean("a\tb\n\x00c  d 🙂"); got != "a b c d ?" {
		t.Errorf("clean = %q", got)
	}
}

func checkGrid(t *testing.T, d Doc) {
	t.Helper()
	for i, l := range d.Lines {
		limit := Cols
		if l.Kind == Header {
			limit = Cols / 2
		}
		if w := width(l.Text); w > limit {
			t.Errorf("line %d is %d cells, max %d: %q", i, w, limit, l.Text)
		}
		for _, a := range l.Aside {
			if width(a) > stampAside {
				t.Errorf("stamp aside %q is wider than %d", a, stampAside)
			}
		}
		for _, r := range l.Text {
			if r != ' ' && !has(r) {
				t.Errorf("line %d has a glyph Go Mono lacks: %q", i, r)
			}
		}
	}
}

func TestDeviceLayout(t *testing.T) {
	d := Device(samsungTV(), Options{Now: printed})
	checkGrid(t, d)
	s := d.plain()
	for _, want := range []string{
		"DEVICE   Living Room TV",
		"KIND     Smart TV · Samsung",
		"PERIOD   Sep 25 – Oct 2, 2026",
		"PRINTED  Oct 2 14:05",
		"CONTENT RECOGNITION ...............  9,812",
		"  › every 15s, 24/7 (heartbeat)",
		"WHILE YOU SLEPT 01:00–06:00 .........  412",
		"Samsung Electronics (KR) .........  11,203",
		"! CAN BYPASS YOUR DNS: dns.google",
		"! BYPASSES YOUR DNS: 1.1.1.1:853",
		"TOTAL CALLS HOME .................  14,302",
		"ABOUT YOU (SNOOPING) ................  71%",
		"SNOOPING PER DAY ..................  1,402",
		"GRADE: F",
		"FIX: Turn off Viewing Information Services",
		"THANK YOU FOR YOUR DATA",
		repo,
	} {
		if !strings.Contains(s, want) {
			t.Errorf("receipt lacks %q\n%s", want, s)
		}
	}
	if strings.Contains(s, "Akamai") {
		t.Error("more than five recipients listed")
	}
	if strings.Contains(s, "DEMO") {
		t.Error("non-demo receipt mentions DEMO")
	}
	// Worst first: ACR before advertising before telemetry.
	if a, b := strings.Index(s, "CONTENT RECOGNITION"), strings.Index(s, "ADVERTISING"); a > b {
		t.Error("categories not worst first")
	}
}

func TestDeviceSkipsZeroCategories(t *testing.T) {
	s := Device(hueBridge(), Options{Now: printed}).plain()
	for _, no := range []string{"CONTENT RECOGNITION", "ADVERTISING", "FIX:", "24/7", "WHILE YOU SLEPT"} {
		if strings.Contains(s, no) {
			t.Errorf("quiet receipt contains %q", no)
		}
	}
}

func TestEmptyReport(t *testing.T) {
	d := Device(model.DeviceReport{}, Options{Now: printed})
	checkGrid(t, d)
	if s := d.plain(); !strings.Contains(s, "nothing heard") || !strings.Contains(s, "GRADE: ?") {
		t.Errorf("empty receipt:\n%s", s)
	}
	checkGrid(t, Home(model.HomeReport{}, Options{Now: printed}))
}

func TestLongNameAndEscaping(t *testing.T) {
	d := Device(longName(), Options{Now: printed})
	checkGrid(t, d)
	s := d.plain()
	if !strings.Contains(s, `DEVICE   Grandma's "Smart" <Fridge> &`) {
		t.Errorf("long name not wrapped as expected:\n%s", s)
	}
	if !strings.Contains(s, "An Extremely Long Company… (NL) ...  2,016") {
		t.Errorf("company not truncated:\n%s", s)
	}
	svg := string(d.SVG())
	if strings.Contains(svg, "<Fridge>") || !strings.Contains(svg, "&lt;Fridge&gt; &amp;") || !strings.Contains(svg, "&#34;Smart&#34;") {
		t.Error("device name not escaped in SVG")
	}
}

func TestHomeLayout(t *testing.T) {
	d := Home(home(), Options{Now: printed})
	checkGrid(t, d)
	if !d.Demo {
		t.Error("HomeReport.Demo must force the watermark")
	}
	s := d.plain()
	for _, want := range []string{
		"YOUR HOME",
		"DEMO DATA · NOT A MEASUREMENT",
		"DEVICE .....................  CALLS  GRADE",
		"Living Room TV ............  14,302    F",
		"Front Door Doorbell Came… ..  3,900    C",
		"WORST OFFENDER  Living Room TV (F)",
		"Amazon (US) ......................  11,133",
	} {
		if !strings.Contains(s, want) {
			t.Errorf("home receipt lacks %q\n%s", want, s)
		}
	}
}

func TestHomeCapsDevices(t *testing.T) {
	r := home()
	for len(r.Devices) < 30 {
		r.Devices = append(r.Devices, hueBridge())
	}
	s := Home(r, Options{Now: printed}).plain()
	if !strings.Contains(s, "+ 10 more devices") {
		t.Errorf("device list not capped:\n%s", s)
	}
}

func TestLeaderNeverTouches(t *testing.T) {
	for _, key := range []string{"", "ESSENTIAL", "BLOCKED BY YOUR FILTER", strings.Repeat("W", 80)} {
		for _, val := range []string{"7", "1,204", "13,381", "1,234,567,890", "CALLS  GRADE"} {
			got := leader(key, val, Cols)
			if width(got) != Cols {
				t.Errorf("leader(%q, %q) is %d cells", key, val, width(got))
			}
			i := strings.Index(got, " ..")
			j := strings.LastIndex(got, ".  "+val)
			if i < 0 || j < 0 || !strings.HasSuffix(got, val) {
				t.Errorf("leader(%q, %q) = %q: dots must be spaced from both sides", key, val, got)
			}
		}
	}
}

func TestCountryNotRepeated(t *testing.T) {
	r := samsungTV()
	r.Companies = []model.CompanyStat{
		{ID: "nielsen", Name: "The Nielsen Company (US), LLC", Country: "US", Count: 9},
		{ID: "ex", Name: "Example GmbH (Germany)", Country: "DE", Count: 8},
		{ID: "lower", Name: "Acme (us)", Country: "US", Count: 7},
		{ID: "plain", Name: "Plain Corp", Country: "FR", Count: 6},
	}
	s := Device(r, Options{Now: printed}).plain()
	for _, want := range []string{"The Nielsen Company (US), LLC ..", "Example GmbH (Germany) ..", "Acme (us) ..", "Plain Corp (FR) .."} {
		if !strings.Contains(s, want) {
			t.Errorf("receipt lacks %q\n%s", want, s)
		}
	}
}

// The home stamp prints HomeReport.Grade as computed by analyze; it must not
// re-derive a grade from the device list.
func TestHomeStampUsesHomeGrade(t *testing.T) {
	stamp := func(r model.HomeReport) string {
		for _, l := range Home(r, Options{Now: printed}).Lines {
			if l.Kind == Stamp {
				return l.Text
			}
		}
		t.Fatal("home receipt has no stamp")
		return ""
	}
	r := home()
	if got := stamp(r); got != "F" {
		t.Errorf("stamp = %q, want F", got)
	}
	r.Grade = "B"
	if got := stamp(r); got != "B" {
		t.Errorf("stamp = %q, want the home grade B, not the worst device's", got)
	}
	if got := stamp(model.HomeReport{}); got != "?" {
		t.Errorf("empty home stamp = %q, want ?", got)
	}
}

// The stamp's headline comes from the rule that decided the grade, and the
// reason is printed under it, so a D for a DNS bypass never claims the
// device "talks about you a lot".
func TestGradeExplained(t *testing.T) {
	lowCoverage := hueBridge()
	lowCoverage.ByCategory[model.CatUnknown] = 3000
	lowCoverage.Total += 3000
	provisional := hueBridge()
	provisional.Reason.Provisional, provisional.Reason.Data = true, 3*time.Hour
	tests := []struct {
		name     string
		r        model.DeviceReport
		want, no []string
	}{
		{"ACR heartbeat", samsungTV(),
			[]string{"WATCHES WHAT YOU WATCH,", "ON A CLOCK.", "WHY: Content recognition (ACR) on a clock,", "always F."},
			[]string{"CAN'T STOP TALKING", "Not all TV platforms"}},
		{"bypass", fixedTV(),
			[]string{"GOES AROUND", "YOUR DNS FILTER.", "WHY: Bypasses your DNS via 1.1.1.1:853: at", "would be C).",
				"No known content-recognition server seen.", "Not all TV platforms' ACR servers are"},
			[]string{"TALKS ABOUT YOU"}},
		{"volume", hueBridge(),
			[]string{"MINDS ITS OWN", "WHY: 4 snooping lookups a day (A is under"},
			[]string{"Graded on", "Provisional", "content-recognition"}},
		{"low coverage", lowCoverage,
			[]string{"Graded on the 40% of lookups phonehome", "recognises."}, nil},
		{"provisional", provisional,
			[]string{"Provisional: based on 3 hours of data."}, nil},
	}
	for _, tt := range tests {
		d := Device(tt.r, Options{Now: printed})
		checkGrid(t, d)
		s := d.plain()
		for _, w := range tt.want {
			if !strings.Contains(s, w) {
				t.Errorf("%s: receipt lacks %q\n%s", tt.name, w, s)
			}
		}
		for _, w := range tt.no {
			if strings.Contains(s, w) {
				t.Errorf("%s: receipt has %q\n%s", tt.name, w, s)
			}
		}
		if a, b := strings.Index(s, "GRADE: "), strings.Index(s, "WHY: "); a < 0 || b < a {
			t.Errorf("%s: reason not under the stamp\n%s", tt.name, s)
		}
	}
	for _, rule := range []string{model.ReasonACRHeartbeat, model.ReasonACR, model.ReasonBypass} {
		for _, l := range headline("D", model.GradeReason{Rule: rule}) {
			if width(l) > stampAside {
				t.Errorf("%s headline %q is wider than %d", rule, l, stampAside)
			}
		}
	}
}

func TestSinceBlock(t *testing.T) {
	d := Device(fixedTV(), Options{Now: printed})
	checkGrid(t, d)
	s := d.plain()
	for _, want := range []string{
		"SINCE LAST WEEK",
		"SNOOPING PER DAY ............  1,402 → 547",
		"  CHANGE ..........................  ↓ 61%",
		"GRADE .............................  F → D",
		"HEARTBEATS STOPPED",
		"  › acr-eu-prd.samsungcloudsolution.com",
		"  › log-ingestion.samsungacr.com",
	} {
		if !strings.Contains(s, want) {
			t.Errorf("receipt lacks %q\n%s", want, s)
		}
	}
	// The block sits between the totals and the stamp.
	if a, b, c := strings.Index(s, "TOTAL CALLS HOME"), strings.Index(s, "SINCE LAST WEEK"), strings.Index(s, "GRADE: "); a > b || b > c {
		t.Errorf("block out of place:\n%s", s)
	}

	h := Home(fixedHome(), Options{Now: printed})
	checkGrid(t, h)
	hs := h.plain()
	for _, want := range []string{"SINCE LAST WEEK", "HOME GRADE ........................  F → D", "  › acr-eu-prd.samsungcloudsolution.com"} {
		if !strings.Contains(hs, want) {
			t.Errorf("home receipt lacks %q\n%s", want, hs)
		}
	}

	// No comparison, no block.
	if strings.Contains(Device(samsungTV(), Options{Now: printed}).plain(), "SINCE") ||
		strings.Contains(Home(home(), Options{Now: printed}).plain(), "SINCE") {
		t.Error("receipt without a comparison prints one")
	}
}

func TestSinceBlockVariants(t *testing.T) {
	r := fixedTV()
	r.Previous.Partial, r.Previous.Days = true, 4.25
	s := Device(r, Options{Now: printed}).plain()
	if !strings.Contains(s, "(ONLY 4.2 OF THOSE 7 DAYS HAVE DATA)") {
		t.Errorf("partial comparison not flagged:\n%s", s)
	}

	r = fixedTV()
	r.Previous = &model.Comparison{Period: r.Period.Previous(), NowPerDay: r.PerDay}
	s = Device(r, Options{Now: printed}).plain()
	if !strings.Contains(s, "NOT SEEN IN THE PERIOD BEFORE") || strings.Contains(s, "CHANGE") {
		t.Errorf("new device:\n%s", s)
	}

	for days, want := range map[int]string{1: "SINCE YESTERDAY", 7: "SINCE LAST WEEK", 30: "VS THE 30 DAYS BEFORE"} {
		p := model.Period{From: printed.Add(-time.Duration(days) * 24 * time.Hour), To: printed}
		if got := sinceTitle(p); got != want {
			t.Errorf("sinceTitle(%d days) = %q, want %q", days, got, want)
		}
	}
	for _, c := range []struct {
		c    model.Comparison
		want string
	}{
		{model.Comparison{Seen: true, PerDay: 100, NowPerDay: 8}, "↓ 92%"},
		{model.Comparison{Seen: true, PerDay: 100, NowPerDay: 150}, "↑ 50%"},
		{model.Comparison{Seen: true, PerDay: 100, NowPerDay: 100.2}, "NO CHANGE"},
		{model.Comparison{Seen: true, PerDay: 0, NowPerDay: 0}, "NO CHANGE"},
		{model.Comparison{Seen: true, PerDay: 0, NowPerDay: 3}, "UP FROM NONE"},
	} {
		if got := change(c.c); got != c.want {
			t.Errorf("change(%+v) = %q, want %q", c.c, got, c.want)
		}
	}
}
