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
