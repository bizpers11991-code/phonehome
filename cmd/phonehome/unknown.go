package main

import (
	"cmp"
	"context"
	"fmt"
	"io"
	"os"
	"slices"
	"time"

	"github.com/bizpers11991-code/phonehome/internal/model"
)

// reportURL is where people tell us about domains the knowledge base lacks.
const reportURL = "https://github.com/bizpers11991-code/phonehome/issues/new?template=new-device.yml"

func cmdUnknown(ctx context.Context, args []string) error {
	fl := newFlags("unknown", "[--config FILE | --demo] [--days N] [--device ID] [--limit N]", "List looked-up domains the knowledge base cannot explain yet, to suggest as rules.")
	cfgPath := fl.String("config", "", configHelp)
	days := fl.Int("days", 7, "how many days back to cover, ending now")
	device := fl.String("device", "", "only this device (ID or name)")
	useDemo := fl.Bool("demo", false, "use the synthetic demo household")
	limit := fl.Int("limit", 10, "domain groups to show per device (0 = all)")
	if err := parseFlags(fl, args); err != nil {
		return err
	}
	if err := checkDays(*days); err != nil {
		return err
	}
	a, done, err := reportApp(ctx, *cfgPath, *useDemo)
	if err != nil {
		return err
	}
	defer done()
	r, err := a.Report(ctx, lastDays(*days))
	if err != nil {
		return err
	}
	if *device != "" {
		id, err := resolveDevice(r, *device)
		if err != nil {
			return err
		}
		dr, _ := findDevice(r, id)
		r.Devices = []model.DeviceReport{dr}
	}
	printUnknown(os.Stdout, r, *days, *limit, a.location())
	return nil
}

// unknownGroup is the unclassified domains of one device under one
// registrable domain.
type unknownGroup struct {
	name        string
	count       int
	first, last time.Time
	domains     []model.UnknownDomain
}

func groupUnknown(us []model.UnknownDomain) []unknownGroup {
	idx := map[string]int{}
	var gs []unknownGroup
	for _, u := range us {
		key := cmp.Or(u.Group, u.Domain)
		i, ok := idx[key]
		if !ok {
			i = len(gs)
			idx[key] = i
			gs = append(gs, unknownGroup{name: key, first: u.First, last: u.Last})
		}
		g := &gs[i]
		g.count += u.Count
		g.domains = append(g.domains, u)
		if !u.First.IsZero() && (g.first.IsZero() || u.First.Before(g.first)) {
			g.first = u.First
		}
		if u.Last.After(g.last) {
			g.last = u.Last
		}
	}
	slices.SortStableFunc(gs, func(x, y unknownGroup) int {
		return cmp.Or(cmp.Compare(y.count, x.count), cmp.Compare(x.name, y.name))
	})
	return gs
}

func printUnknown(w io.Writer, r model.HomeReport, days, limit int, loc *time.Location) {
	if r.Demo {
		fmt.Fprintln(w, "DEMO DATA — a synthetic household, not a measurement.")
		fmt.Fprintln(w)
	}
	stamp := func(t time.Time) string {
		if t.IsZero() {
			return "-"
		}
		return t.In(loc).Format("Jan 02 15:04")
	}
	found := false
	for _, d := range r.Devices {
		if len(d.Unknown) == 0 {
			continue
		}
		if !found {
			fmt.Fprintf(w, "Domains the knowledge base can't explain yet, last %d days.\n", days)
			fmt.Fprintf(w, "Local names and reverse lookups are left out.\n")
		}
		found = true
		total := 0
		for _, u := range d.Unknown {
			total += u.Count
		}
		fmt.Fprintf(w, "\n%s  (%s)  %s lookups\n", printable(d.Device.DisplayName()), printable(deviceKind(d.Device)), thousands(total))
		fmt.Fprintf(w, "  %8s  %-12s  %-12s  %s\n", "lookups", "first seen", "last seen", "domain")
		gs := groupUnknown(d.Unknown)
		shown := gs
		if limit > 0 && len(gs) > limit {
			shown = gs[:limit]
		}
		for _, g := range shown {
			if len(g.domains) == 1 { // nothing to group: show the name itself
				fmt.Fprintf(w, "  %8s  %-12s  %-12s  %s\n", thousands(g.count), stamp(g.first), stamp(g.last), printable(g.domains[0].Domain))
				continue
			}
			fmt.Fprintf(w, "  %8s  %-12s  %-12s  %s  (%d names)\n", thousands(g.count), stamp(g.first), stamp(g.last), printable(g.name), len(g.domains))
			for _, u := range g.domains {
				fmt.Fprintf(w, "  %8s  %-12s  %-12s    %s\n", thousands(u.Count), stamp(u.First), stamp(u.Last), printable(u.Domain))
			}
		}
		if n := len(gs) - len(shown); n > 0 {
			fmt.Fprintf(w, "  … and %d more (use --limit 0 to see all)\n", n)
		}
	}
	if !found {
		fmt.Fprintf(w, "No unclassified domains in the last %d days: the knowledge base explains every lookup.\n", days)
		return
	}
	fmt.Fprintf(w, "\nKnow what one of these is for? Tell us, with evidence, so everyone's report gets better:\n  %s\n", reportURL)
	fmt.Fprintln(w, "The dashboard's device details have a prefilled \"Suggest a rule\" link. Issues are public:")
	fmt.Fprintln(w, "share domain names and the device's make, never addresses, hostnames or account IDs.")
}

// deviceKind describes a device without private details: "Samsung · tv".
func deviceKind(d model.Device) string {
	vendor := cmp.Or(d.Vendor, "unknown vendor")
	kind := d.Kind
	if kind == "" {
		kind = model.KindUnknown
	}
	return vendor + " · " + string(kind)
}
