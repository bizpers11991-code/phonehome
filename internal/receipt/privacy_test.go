package receipt

import (
	"net/netip"
	"strings"
	"testing"
	"time"

	"github.com/bizpers11991-code/phonehome/internal/model"
)

// TestReceiptPrintsNoHouseholdNames checks every receipt that prints domain
// names (heartbeats, and heartbeats that stopped) in every form a receipt
// takes: the laid-out lines the PNG is drawn from, and the SVG with its
// <title> and <desc>.
func TestReceiptPrintsNoHouseholdNames(t *testing.T) {
	phone := model.Device{
		ID:  "mac:3c:22:fb:7e:90:aa",
		MAC: "3C:22:FB:7E:90:AA",
		IPs: []netip.Addr{netip.MustParseAddr("192.168.1.105")},
		// The device line prints DisplayName, which can be an address; the
		// names printed are what is under test here.
		Vendor: "Apple",
	}
	beats := []model.Heartbeat{
		{Domain: "annas-iphone.lan", Category: model.CatTelemetry, Every: 5 * time.Minute},
		{Domain: "105.1.168.192.in-addr.arpa", Category: model.CatTelemetry, Every: 5 * time.Minute},
		{Domain: "3c22fb7e90aa.tracker.example", Category: model.CatTracking, Every: 5 * time.Minute},
		{Domain: "3c-22-fb-7e-90-aa.tracker.example", Category: model.CatTracking, Every: 5 * time.Minute},
		{Domain: "host-192-168-1-105.isp.example", Category: model.CatTracking, Every: 5 * time.Minute},
		{Domain: "a1b2c3d4e5f6.metrics.vendor.example", Category: model.CatTelemetry, Every: time.Minute},
		{Domain: "log.vendor.example", Category: model.CatTelemetry, Every: 10 * time.Minute},
		{Domain: "ads.vendor.example", Category: model.CatAds, Every: 10 * time.Minute},
	}
	device := func() model.DeviceReport {
		return model.DeviceReport{
			Device: phone, Period: lastWeek, Total: 900, Grade: "C",
			ByCategory: map[model.Category]int{model.CatTracking: 300, model.CatTelemetry: 500, model.CatAds: 100},
			Heartbeats: beats,
		}
	}
	since := func() model.DeviceReport {
		r := device()
		r.Heartbeats = nil
		r.Previous = &model.Comparison{Period: lastWeek.Previous(), Days: 7, Seen: true, Grade: "D", Stopped: beats}
		return r
	}
	homeSince := func() model.HomeReport {
		r := since()
		return model.HomeReport{
			Period: lastWeek, Devices: []model.DeviceReport{r}, Total: r.Total, ByCategory: r.ByCategory, Grade: "C",
			Previous: &model.HomeComparison{Comparison: *r.Previous},
		}
	}

	for _, tt := range []struct {
		name string
		doc  Doc
		want []string
	}{
		{"device", Device(device(), Options{Now: printed}), []string{
			"    *.metrics.vendor.example", "    log.vendor.example", "    ads.vendor.example",
		}},
		{"device since", Device(since(), Options{Now: printed}), []string{
			"HEARTBEATS STOPPED", "  › ads.vendor.example", "  › *.metrics.vendor.example", "  › log.vendor.example",
		}},
		{"home since", Home(homeSince(), Options{Now: printed}), []string{
			"HEARTBEATS STOPPED", "  › ads.vendor.example", "  › *.metrics.vendor.example", "  › log.vendor.example",
		}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			checkGrid(t, tt.doc)
			plain := tt.doc.plain()
			for _, w := range tt.want {
				if !strings.Contains(plain, w) {
					t.Errorf("receipt lacks %q\n%s", w, plain)
				}
			}
			var all strings.Builder
			all.WriteString(tt.doc.Title)
			for _, l := range tt.doc.Lines {
				all.WriteString("\n" + l.Text + "\n" + strings.Join(l.Aside, "\n"))
			}
			all.Write(tt.doc.SVG())
			out := strings.ToLower(all.String())
			for _, secret := range []string{
				"annas-iphone", ".lan", "in-addr.arpa", "105.1.168.192", "192-168", "192.168",
				"3c22fb7e90aa", "3c-22-fb", "3c:22:fb", "a1b2c3d4",
			} {
				if strings.Contains(out, secret) {
					t.Errorf("receipt contains %q\n%s", secret, plain)
				}
			}
		})
	}
}
