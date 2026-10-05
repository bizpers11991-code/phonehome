package receipt

import (
	"fmt"
	"math"

	"github.com/bizpers11991-code/phonehome/internal/model"
	"github.com/bizpers11991-code/phonehome/internal/redact"
)

// sinceTitle names the comparison for the block heading: "SINCE LAST WEEK".
func sinceTitle(p model.Period) string {
	switch d := p.Days(); {
	case d == 1:
		return "SINCE YESTERDAY"
	case d == 7:
		return "SINCE LAST WEEK"
	case d == math.Trunc(d):
		return fmt.Sprintf("VS THE %.0f DAYS BEFORE", d)
	default:
		return fmt.Sprintf("VS THE %.1f DAYS BEFORE", d)
	}
}

// change is the receipt's wording for a change in snooping per day.
func change(c model.Comparison) string {
	ch, ok := c.Change()
	if !ok {
		return "UP FROM NONE"
	}
	switch p := math.Round(ch * 100); {
	case p < 0:
		return fmt.Sprintf("↓ %.0f%%", -p)
	case p > 0:
		return fmt.Sprintf("↑ %.0f%%", p)
	}
	return "NO CHANGE"
}

func gradeOrQ(g string) string {
	g = fit(clean(g), 1)
	if g == "" {
		return "?"
	}
	return g
}

// since prints the "SINCE LAST WEEK" block: snooping per day and grade
// before → now, and the snooping heartbeats that stopped. now is the current
// grade; stopped lists heartbeats to report (at most three are printed).
func (b *builder) since(c *model.Comparison, gradeKey, now string, stopped []model.Heartbeat) {
	if c == nil {
		return
	}
	b.kind(Rule)
	b.bold(sinceTitle(c.Period))
	if c.Partial {
		b.text(fmt.Sprintf("  (ONLY %s OF THOSE %s DAYS HAVE DATA)", rate(math.Floor(c.Days*10)/10), rate(c.Period.Days())))
	}
	if !c.Seen {
		b.text("  NOT SEEN IN THE PERIOD BEFORE")
		return
	}
	b.text(leader("SNOOPING PER DAY", rate(c.PerDay)+" → "+rate(c.NowPerDay), Cols))
	b.text(leader("  CHANGE", change(*c), Cols))
	b.bold(leader(gradeKey, gradeOrQ(c.Grade)+" → "+gradeOrQ(now), Cols))
	if len(stopped) == 0 {
		return
	}
	b.text("HEARTBEATS STOPPED")
	for i, h := range stopped {
		if i == 3 {
			b.text(fmt.Sprintf("  + %d more", len(stopped)-3))
			break
		}
		b.text("  " + marker + " " + fit(clean(h.Domain), Cols-4))
	}
}

// homeStopped gathers the printable stopped heartbeats of every device in a
// home, worst category first.
func homeStopped(r model.HomeReport) []model.Heartbeat {
	var out []model.Heartbeat
	for _, cat := range model.Categories() {
		for _, d := range r.Devices {
			if d.Previous == nil {
				continue
			}
			for _, h := range printable(d.Previous.Stopped, d.Device) {
				if h.Category == cat {
					out = append(out, h)
				}
			}
		}
	}
	return out
}

// printable returns the heartbeats of device d whose names may be printed on
// a receipt, redacted (redact.Domain); the others are left out. A receipt is
// made to be shared, so it must not carry local names, reverse lookups, or
// the device's MAC, address or serial number.
func printable(hs []model.Heartbeat, d model.Device) []model.Heartbeat {
	out := make([]model.Heartbeat, 0, len(hs))
	for _, h := range hs {
		if h.Domain = redact.Domain(h.Domain, d); h.Domain != "" {
			out = append(out, h)
		}
	}
	return out
}
