package receipt

import (
	"net/netip"
	"time"

	"github.com/bizpers11991-code/phonehome/internal/model"
)

var (
	berlin   = time.FixedZone("CEST", 2*3600)
	printed  = time.Date(2026, 10, 2, 14, 5, 0, 0, berlin)
	lastWeek = model.Period{From: time.Date(2026, 9, 25, 0, 0, 0, 0, berlin), To: time.Date(2026, 10, 2, 14, 5, 0, 0, berlin)}
)

func hourly(n int) (h [24]int) {
	for i := range h {
		h[i] = n
	}
	return h
}

// samsungTV is a nasty smart TV: ACR heartbeat, ads, DoH bypass.
func samsungTV() model.DeviceReport {
	by := map[model.Category]int{
		model.CatACR:       9812,
		model.CatAds:       2104,
		model.CatTracking:  811,
		model.CatTelemetry: 912,
		model.CatEssential: 402,
		model.CatContent:   210,
		model.CatUnknown:   51,
	}
	return model.DeviceReport{
		Device: model.Device{
			ID: "mac:8c:79:f5:00:11:22", MAC: "8c:79:f5:00:11:22",
			IPs:    []netip.Addr{netip.MustParseAddr("192.168.1.23")},
			Vendor: "Samsung", Kind: model.KindTV, Label: "Living Room TV",
		},
		Period:     lastWeek,
		Total:      14302,
		Blocked:    1204,
		ByCategory: by,
		Snooping:   13639,
		SnoopShare: 0.71,
		PerDay:     1402.4,
		Hourly:     hourly(500),
		Companies: []model.CompanyStat{
			{ID: "samsung", Name: "Samsung Electronics", Country: "KR", Count: 11203},
			{ID: "google", Name: "Google", Country: "US", Count: 902},
			{ID: "amazon", Name: "Amazon", Country: "US", Count: 611},
			{ID: "nielsen", Name: "The Nielsen Company (US), LLC", Country: "US", Count: 402},
			{ID: "samba", Name: "Samba TV", Country: "US", Count: 133},
			{ID: "akamai", Name: "Akamai", Country: "US", Count: 99},
		},
		Heartbeats: []model.Heartbeat{
			{Domain: "acr-eu-prd.samsungcloudsolution.com", Category: model.CatACR, Count: 9600, Every: 15 * time.Second, Jitter: 0.02},
			{Domain: "log-config.samsungacr.com", Category: model.CatTelemetry, Count: 2016, Every: 4*time.Minute + 50*time.Second, Jitter: 0.1},
		},
		QuietHours: 412,
		QuietLabel: "01:00–06:00",
		Bypasses: []model.Bypass{
			{Kind: "doh-lookup", Detail: "Looked up a DNS-over-HTTPS resolver", Evidence: "dns.google", Confidence: "high"},
			{Kind: "dot-flow", Detail: "Connected to a DNS-over-TLS server", Evidence: "1.1.1.1:853", Confidence: "high"},
		},
		Fixes: []model.Fix{
			{ID: "samsung-tv-viewing-info", Title: "Turn off Viewing Information Services (Samsung's ACR)"},
			{ID: "samsung-tv-ads", Title: "Turn off interest-based ads"},
			{ID: "samsung-tv-voice", Title: "Turn off voice recognition"},
		},
		Grade: "F",
	}
}

// hueBridge is a well-behaved hub.
func hueBridge() model.DeviceReport {
	return model.DeviceReport{
		Device: model.Device{
			ID: "mac:00:17:88:aa:bb:cc", Vendor: "Signify", Kind: model.KindHub, Hostname: "Philips-hue",
		},
		Period:     lastWeek,
		Total:      2016,
		ByCategory: map[model.Category]int{model.CatEssential: 1990, model.CatTelemetry: 26},
		Snooping:   26,
		SnoopShare: 26.0 / 2016,
		PerDay:     3.6,
		Companies:  []model.CompanyStat{{ID: "signify", Name: "Signify", Country: "NL", Count: 2016}},
		Heartbeats: []model.Heartbeat{
			{Domain: "time.meethue.com", Category: model.CatEssential, Count: 2016, Every: 5 * time.Minute},
		},
		Grade: "A",
	}
}

// longName has a hostile 60+ character label.
func longName() model.DeviceReport {
	r := hueBridge()
	r.Device.Label = `Grandma's "Smart" <Fridge> & Ice-o-Matic 3000 Deluxe Edition™`
	r.Device.Kind = model.KindAppliance
	r.Device.Vendor = "LG Electronics"
	r.Grade = "C"
	r.Companies[0].Name = "An Extremely Long Company Name Holdings International Limited"
	return r
}

func home() model.HomeReport {
	tv := samsungTV()
	dev := func(name string, kind model.DeviceKind, total, snoop int, grade string) model.DeviceReport {
		return model.DeviceReport{
			Device:     model.Device{ID: "mac:" + name, Label: name, Kind: kind},
			Total:      total,
			Snooping:   snoop,
			Grade:      grade,
			ByCategory: map[model.Category]int{model.CatTelemetry: snoop, model.CatEssential: total - snoop},
			Companies:  []model.CompanyStat{{ID: "amazon", Name: "Amazon", Country: "US", Count: snoop}},
		}
	}
	devs := []model.DeviceReport{
		tv,
		dev("Kitchen Echo", model.KindSpeaker, 6120, 4310, "D"),
		dev("Bedroom Fire TV Stick", model.KindStreamer, 5003, 3120, "D"),
		dev("Front Door Doorbell Camera (the one that rings)", model.KindCamera, 3900, 1450, "C"),
		dev("Roborock S7", model.KindVacuum, 1888, 920, "C"),
		dev("Office Printer", model.KindComputer, 760, 410, "B"),
		dev("PlayStation 5", model.KindConsole, 2400, 300, "B"),
		dev("Smart Plug — Kettle", model.KindPlug, 1440, 12, "A"),
		hueBridge(),
	}
	r := model.HomeReport{Period: lastWeek, GeneratedAt: printed, Devices: devs, ByCategory: map[model.Category]int{}, Grade: "F", Demo: true}
	for _, d := range devs {
		r.Total += d.Total
		r.Snooping += d.Snooping
		for c, n := range d.ByCategory {
			r.ByCategory[c] += n
		}
	}
	return r
}
