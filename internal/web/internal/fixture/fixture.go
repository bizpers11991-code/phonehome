// Package fixture is a realistic in-memory web.Backend used by the web
// tests and the dev server. The household and its numbers are invented.
package fixture

import (
	"bytes"
	"cmp"
	"context"
	"fmt"
	"html"
	"image"
	"image/png"
	"math"
	"net/netip"
	"slices"
	"sync"
	"time"

	"github.com/bizpers11991-code/phonehome/internal/model"
)

// Backend serves a fixed household of ten devices.
type Backend struct {
	Demo  bool // mark reports as demo data
	Empty bool // pretend nothing has been ingested yet
	Now   func() time.Time

	mu     sync.Mutex
	labels map[string]string
}

// New returns a Backend whose clock is fixed at now.
func New(now time.Time) *Backend {
	return &Backend{Now: func() time.Time { return now }, labels: map[string]string{}}
}

type dom struct {
	name    string
	perDay  float64
	blocked float64 // share of lookups the resolver blocked
	cat     model.Category
	company string
	purpose string
}

type dev struct {
	model.Device
	shape   func(h int) float64
	grade   string
	doms    []dom
	beats   []model.Heartbeat
	bypass  []model.Bypass
	fixes   []model.Fix
	flowsPD int
}

var companies = map[string]model.Company{
	"samsung":   {ID: "samsung", Name: "Samsung", Country: "KR"},
	"samsungad": {ID: "samsungad", Name: "Samsung Ads", Country: "US"},
	"lg":        {ID: "lg", Name: "LG Electronics", Country: "KR"},
	"alphonso":  {ID: "alphonso", Name: "Alphonso (LG Ad Solutions)", Country: "US"},
	"roku":      {ID: "roku", Name: "Roku", Country: "US"},
	"amazon":    {ID: "amazon", Name: "Amazon", Country: "US"},
	"ring":      {ID: "ring", Name: "Ring (Amazon)", Country: "US"},
	"google":    {ID: "google", Name: "Google", Country: "US"},
	"netflix":   {ID: "netflix", Name: "Netflix", Country: "US"},
	"xiaomi":    {ID: "xiaomi", Name: "Xiaomi", Country: "CN"},
	"roborock":  {ID: "roborock", Name: "Roborock", Country: "CN"},
	"signify":   {ID: "signify", Name: "Signify (Philips Hue)", Country: "NL"},
	"apple":     {ID: "apple", Name: "Apple", Country: "US"},
	"meta":      {ID: "meta", Name: "Meta", Country: "US"},
	"akamai":    {ID: "akamai", Name: "Akamai", Country: "US"},
}

func evening(h int) float64 {
	return 0.25 + math.Exp(-math.Pow(float64(h)-21, 2)/6) + 0.4*math.Exp(-math.Pow(float64(h)-8, 2)/3)
}

func flat(h int) float64 { return 1 + 0.15*math.Sin(float64(h)) }

func daytime(h int) float64 {
	if h < 7 {
		return 0.06
	}
	return 0.6 + 0.4*math.Sin(float64(h-7)/16*math.Pi)
}

func tvAlwaysOn(h int) float64 { return 0.55 + evening(h) }

func household() []dev {
	ip := netip.MustParseAddr
	return []dev{
		{
			Device: model.Device{ID: "mac:f4:7b:09:3c:a1:2e", MAC: "f4:7b:09:3c:a1:2e", IPs: []netip.Addr{ip("192.168.1.31")},
				Hostname: "Samsung-QN65Q80B", Vendor: "Samsung", Kind: model.KindTV, Label: "Living room TV"},
			shape: tvAlwaysOn, grade: "F", flowsPD: 2400,
			doms: []dom{
				{"acr-eu-prd.samsungcloud.tv", 5760, 0, model.CatACR, "samsung", "Sends fingerprints of what is on screen so Samsung can identify what you watch."},
				{"log-ingestion.samsungacr.com", 1440, 0.1, model.CatACR, "samsung", "Uploads content-recognition match logs."},
				{"ads.samsungads.com", 610, 0.55, model.CatAds, "samsungad", "Fetches the ads shown on the home screen."},
				{"config.samsungads.com", 288, 0.4, model.CatAds, "samsungad", "Ad targeting configuration."},
				{"samsung-com.112.2o7.net", 190, 0.8, model.CatTracking, "samsungad", "Adobe analytics beacon used for cross-device audience building."},
				{"osb-eusvc.samsungqbe.com", 330, 0, model.CatTelemetry, "samsung", "Usage statistics for the Smart Hub."},
				{"dns.google", 96, 0, model.CatEssential, "google", "Google's encrypted DNS resolver."},
				{"time.samsungcloudsolution.com", 48, 0, model.CatEssential, "samsung", "Clock sync."},
				{"nflxvideo.net", 420, 0, model.CatContent, "netflix", "Netflix video streams."},
				{"api-global.netflix.com", 160, 0, model.CatContent, "netflix", "Netflix app."},
			},
			beats: []model.Heartbeat{
				{Domain: "acr-eu-prd.samsungcloud.tv", Category: model.CatACR, Count: 40320, Every: 15 * time.Second, Jitter: 0.04},
				{Domain: "log-ingestion.samsungacr.com", Category: model.CatACR, Count: 10080, Every: time.Minute, Jitter: 0.09},
			},
			bypass: []model.Bypass{{Kind: "doh-lookup", Detail: "Looked up Google's DNS-over-HTTPS resolver, so it can resolve names without asking your Pi-hole.", Evidence: "dns.google", Confidence: "high"}},
			fixes: []model.Fix{
				{ID: "samsung-tv-viewing-info", Title: "Turn off Viewing Information Services (Samsung's ACR)", Steps: []string{
					"Settings › All Settings › General & Privacy › Terms & Privacy",
					"Viewing Information Services → Off",
					"Interest-Based Advertisement → Off",
				}, Notes: "Menu names vary by model year (2022+ shown).", Evidence: []string{"https://www.consumerreports.org/privacy/how-to-turn-off-smart-tv-snooping-features-a4840102036/"}},
				{ID: "block-doh", Title: "Stop devices from bypassing your DNS", Steps: []string{
					"In Pi-hole, add dns.google and cloudflare-dns.com to a blocklist",
					"On your router, block outbound TCP/UDP port 853 (DNS-over-TLS)",
				}, Evidence: []string{"https://docs.pi-hole.net/"}},
			},
		},
		{
			Device: model.Device{ID: "mac:a8:23:fe:51:0d:77", MAC: "a8:23:fe:51:0d:77", IPs: []netip.Addr{ip("192.168.1.32")},
				Hostname: "LGwebOSTV", Vendor: "LG Electronics", Kind: model.KindTV},
			shape: evening, grade: "D", flowsPD: 900,
			doms: []dom{
				{"tkacr295.alphonso.tv", 2100, 0.2, model.CatACR, "alphonso", "Live TV content recognition (LG Live Plus)."},
				{"us.ad.lgsmartad.com", 520, 0.6, model.CatAds, "lg", "Home-screen and in-app ads."},
				{"us.info.lgsmartad.com", 260, 0.5, model.CatTracking, "lg", "Ad-measurement and viewing profile."},
				{"ngfts.lge.com", 180, 0, model.CatTelemetry, "lg", "webOS usage logs."},
				{"snu.lge.com", 24, 0, model.CatEssential, "lg", "Firmware update check."},
				{"youtube.com", 380, 0, model.CatContent, "google", "YouTube app."},
			},
			beats: []model.Heartbeat{{Domain: "tkacr295.alphonso.tv", Category: model.CatACR, Count: 8640, Every: 10 * time.Second, Jitter: 0.07}},
			fixes: []model.Fix{{ID: "lg-live-plus", Title: "Turn off LG Live Plus and ad tracking", Steps: []string{
				"Settings › All Settings › General › System › Additional Settings",
				"Live Plus → Off",
				"Advertisement › Limit Ad Tracking → On",
			}, Evidence: []string{"https://www.lg.com/us/support/help-library/how-to-turn-off-live-plus"}}},
		},
		{
			Device: model.Device{ID: "mac:d8:31:34:9a:44:02", MAC: "d8:31:34:9a:44:02", IPs: []netip.Addr{ip("192.168.1.40")},
				Hostname: "Roku-Ultra", Vendor: "Roku", Kind: model.KindStreamer},
			shape: evening, grade: "D", flowsPD: 640,
			doms: []dom{
				{"scribe.logs.roku.com", 1150, 0.3, model.CatTelemetry, "roku", "Detailed usage and channel logs."},
				{"giga.logs.roku.com", 560, 0.2, model.CatTelemetry, "roku", "Playback analytics."},
				{"ads.roku.com", 470, 0.7, model.CatAds, "roku", "Roku home-screen ads."},
				{"p.ads.roku.com", 180, 0.7, model.CatTracking, "roku", "Ad attribution pings."},
				{"captive.roku.com", 96, 0, model.CatEssential, "roku", "Connectivity check."},
				{"api.roku.com", 220, 0, model.CatContent, "roku", "Roku channel store and UI."},
				{"nflxvideo.net", 310, 0, model.CatContent, "netflix", "Netflix video streams."},
			},
			beats: []model.Heartbeat{{Domain: "scribe.logs.roku.com", Category: model.CatTelemetry, Count: 10080, Every: time.Minute, Jitter: 0.12}},
			fixes: []model.Fix{{ID: "roku-ad-tracking", Title: "Limit ad tracking on Roku", Steps: []string{
				"Settings › Privacy › Advertising › Limit ad tracking → On",
				"Settings › Privacy › Smart TV experience → uncheck Use info from TV inputs",
			}, Evidence: []string{"https://support.roku.com/article/115008315867"}}},
		},
		{
			Device: model.Device{ID: "mac:68:54:fd:12:9b:c0", MAC: "68:54:fd:12:9b:c0", IPs: []netip.Addr{ip("192.168.1.51")},
				Hostname: "amazon-3f2a1b", Vendor: "Amazon", Kind: model.KindSpeaker, Label: "Kitchen Echo"},
			shape: flat, grade: "C", flowsPD: 1300,
			doms: []dom{
				{"device-metrics-us.amazon.com", 720, 0.4, model.CatTelemetry, "amazon", "Device metrics and usage reporting."},
				{"unagi-na.amazon.com", 420, 0.3, model.CatTelemetry, "amazon", "Alexa diagnostics stream."},
				{"api.amazonalexa.com", 380, 0, model.CatContent, "amazon", "Alexa voice service."},
				{"avs-alexa-14-na.amazon.com", 300, 0, model.CatContent, "amazon", "Alexa voice service."},
				{"spectrum.s3.amazonaws.com", 60, 0, model.CatEssential, "amazon", "Firmware delivery."},
			},
			beats: []model.Heartbeat{{Domain: "device-metrics-us.amazon.com", Category: model.CatTelemetry, Count: 2016, Every: 5 * time.Minute, Jitter: 0.18}},
			fixes: []model.Fix{{ID: "alexa-privacy", Title: "Tighten Alexa privacy settings", Steps: []string{
				"Alexa app › More › Alexa Privacy › Manage Your Alexa Data",
				"Choose how long to save recordings → Don't save recordings",
				"Help improve Alexa → Off",
			}, Evidence: []string{"https://www.amazon.com/alexaprivacy"}}},
		},
		{
			Device: model.Device{ID: "mac:20:df:b9:44:10:8a", MAC: "20:df:b9:44:10:8a", IPs: []netip.Addr{ip("192.168.1.52")},
				Hostname: "Google-Nest-Mini", Vendor: "Google", Kind: model.KindSpeaker},
			shape: flat, grade: "C", flowsPD: 800,
			doms: []dom{
				{"firebaselogging-pa.googleapis.com", 610, 0.2, model.CatTelemetry, "google", "App usage logging."},
				{"play.googleapis.com", 260, 0, model.CatTelemetry, "google", "Play services check-ins and logs."},
				{"connectivitycheck.gstatic.com", 288, 0, model.CatEssential, "google", "Connectivity check."},
				{"clients3.google.com", 180, 0, model.CatContent, "google", "Assistant services."},
			},
			fixes: []model.Fix{{ID: "google-web-app-activity", Title: "Turn off Web & App Activity for Assistant", Steps: []string{
				"myactivity.google.com › Web & App Activity → Off (or auto-delete after 3 months)",
				"Google Home app › Settings › Google Assistant › Your data in the Assistant",
			}, Evidence: []string{"https://support.google.com/assistant/answer/7108196"}}},
		},
		{
			Device: model.Device{ID: "mac:34:3e:a4:8b:2d:19", MAC: "34:3e:a4:8b:2d:19", IPs: []netip.Addr{ip("192.168.1.60")},
				Hostname: "RingDoorbell-19", Vendor: "Ring", Kind: model.KindCamera, Label: "Front door"},
			shape: flat, grade: "B", flowsPD: 500,
			doms: []dom{
				{"device-metrics-us.amazon.com", 288, 0.4, model.CatTelemetry, "amazon", "Device metrics and usage reporting."},
				{"es.ring.com", 340, 0, model.CatContent, "ring", "Motion events and live view."},
				{"fw.ring.com", 24, 0, model.CatEssential, "ring", "Firmware updates."},
			},
		},
		{
			Device: model.Device{ID: "mac:b0:4a:39:6c:e3:51", MAC: "b0:4a:39:6c:e3:51", IPs: []netip.Addr{ip("192.168.1.70")},
				Hostname: "roborock-vacuum-a15", Vendor: "Roborock", Kind: model.KindVacuum},
			shape: daytime, grade: "C", flowsPD: 300,
			doms: []dom{
				{"tracking.intl.miui.com", 140, 0.6, model.CatTracking, "xiaomi", "Xiaomi analytics and device tracking."},
				{"de.api.io.mi.com", 260, 0, model.CatTelemetry, "xiaomi", "Status reports to the Mi Home cloud."},
				{"awsde0.fds.api.xiaomi.com", 60, 0, model.CatContent, "xiaomi", "Map uploads for the app."},
				{"api-eu.roborock.com", 110, 0, model.CatEssential, "roborock", "Cloud control and updates."},
			},
			fixes: []model.Fix{{ID: "roborock-local", Title: "Keep the vacuum's maps at home", Steps: []string{
				"Roborock app › Settings › Privacy → turn off Product improvement program",
				"Consider Valetudo (local-only firmware) if you are comfortable flashing devices",
			}, Notes: "Flashing firmware voids warranties; read the guide first.", Evidence: []string{"https://valetudo.cloud/"}}},
		},
		{
			Device: model.Device{ID: "mac:ec:b5:fa:0e:7d:42", MAC: "ec:b5:fa:0e:7d:42", IPs: []netip.Addr{ip("192.168.1.80")},
				Hostname: "Philips-hue", Vendor: "Signify", Kind: model.KindHub},
			shape: flat, grade: "A", flowsPD: 150,
			doms: []dom{
				{"diag.meethue.com", 24, 0, model.CatTelemetry, "signify", "Bridge diagnostics."},
				{"ws.meethue.com", 288, 0, model.CatEssential, "signify", "Remote control from the Hue app."},
				{"time.meethue.com", 24, 0, model.CatEssential, "signify", "Clock sync."},
			},
		},
		{
			Device: model.Device{ID: "mac:da:a1:19:5e:62:0b", MAC: "da:a1:19:5e:62:0b", IPs: []netip.Addr{ip("192.168.1.104"), ip("fe80::d8a1:19ff:fe5e:620b")},
				Hostname: "Marias-iPhone", Kind: model.KindPhone},
			shape: daytime, grade: "C", flowsPD: 2100,
			doms: []dom{
				{"app-measurement.com", 310, 0.9, model.CatTracking, "google", "Firebase analytics inside apps."},
				{"graph.facebook.com", 240, 0.5, model.CatTracking, "meta", "Facebook SDK inside apps."},
				{"googleads.g.doubleclick.net", 180, 0.95, model.CatAds, "google", "In-app ads."},
				{"mesu.apple.com", 12, 0, model.CatEssential, "apple", "iOS update check."},
				{"gateway.icloud.com", 420, 0, model.CatEssential, "apple", "iCloud."},
				{"scontent.cdninstagram.com", 900, 0, model.CatContent, "meta", "Instagram photos and video."},
				{"api.spotify.com", 300, 0, model.CatContent, "", ""},
			},
		},
		{
			Device: model.Device{ID: "mac:3c:22:fb:7e:90:aa", MAC: "3c:22:fb:7e:90:aa", IPs: []netip.Addr{ip("192.168.1.105")},
				Hostname: "Dans-MacBook-Air", Vendor: "Apple", Kind: model.KindComputer},
			shape: daytime, grade: "A", flowsPD: 3500,
			doms: []dom{
				{"xp.apple.com", 60, 0, model.CatTelemetry, "apple", "Apple analytics (opt-in)."},
				{"gateway.icloud.com", 300, 0, model.CatEssential, "apple", "iCloud."},
				{"github.com", 520, 0, model.CatContent, "", ""},
				{"a1234.dscb.akamai.net", 410, 0, model.CatContent, "akamai", "Content delivery."},
				{"cdn.example-unknown.io", 40, 0, model.CatUnknown, "", ""},
			},
		},
	}
}

func (b *Backend) Report(_ context.Context, p model.Period) (model.HomeReport, error) {
	rep := model.HomeReport{Period: p, GeneratedAt: b.Now(), Demo: b.Demo, ByCategory: map[model.Category]int{}}
	if b.Empty {
		return rep, nil
	}
	days := p.Days()
	b.mu.Lock()
	defer b.mu.Unlock()
	for _, d := range household() {
		if l, ok := b.labels[d.ID]; ok {
			d.Label = l
		}
		r := build(d, p, days)
		rep.Devices = append(rep.Devices, r)
		rep.Total += r.Total
		rep.Snooping += r.Snooping
		for c, n := range r.ByCategory {
			rep.ByCategory[c] += n
		}
	}
	slices.SortStableFunc(rep.Devices, func(a, b model.DeviceReport) int { return cmp.Compare(b.Snooping, a.Snooping) })
	return rep, nil
}

func build(d dev, p model.Period, days float64) model.DeviceReport {
	r := model.DeviceReport{
		Device: d.Device, Period: p, ByCategory: map[model.Category]int{}, Grade: d.grade,
		QuietLabel: "01:00–06:00", Bypasses: d.bypass, Fixes: d.fixes, Flows: int(float64(d.flowsPD) * days),
	}
	byCompany := map[string]int{}
	for _, dm := range d.doms {
		n := int(dm.perDay * days)
		blocked := int(float64(n) * dm.blocked)
		r.Total += n
		r.Blocked += blocked
		r.ByCategory[dm.cat] += n
		if dm.cat.Snooping() {
			r.Snooping += n
		}
		c := companies[dm.company]
		r.TopDomains = append(r.TopDomains, model.DomainStat{Domain: dm.name, Count: n, Blocked: blocked,
			Category: dm.cat, CompanyID: c.ID, CompanyName: c.Name, Purpose: dm.purpose})
		if c.ID != "" {
			byCompany[c.ID] += n
		}
	}
	slices.SortStableFunc(r.TopDomains, func(a, b model.DomainStat) int { return cmp.Compare(b.Count, a.Count) })
	for id, n := range byCompany {
		c := companies[id]
		r.Companies = append(r.Companies, model.CompanyStat{ID: id, Name: c.Name, Country: c.Country, Count: n})
	}
	slices.SortFunc(r.Companies, func(a, b model.CompanyStat) int {
		return cmp.Or(cmp.Compare(b.Count, a.Count), cmp.Compare(a.ID, b.ID))
	})
	for _, c := range r.Companies {
		if !slices.Contains(r.Countries, c.Country) {
			r.Countries = append(r.Countries, c.Country)
		}
	}
	var sum float64
	for h := range 24 {
		sum += d.shape(h)
	}
	for h := range 24 {
		r.Hourly[h] = int(float64(r.Total) * d.shape(h) / sum)
		if h >= 1 && h < 6 {
			r.QuietHours += r.Hourly[h]
		}
	}
	for _, hb := range d.beats {
		hb.Count = int(float64(hb.Count) / 7 * days)
		r.Heartbeats = append(r.Heartbeats, hb)
	}
	if r.Total > 0 {
		r.SnoopShare = float64(r.Snooping) / float64(r.Total)
	}
	r.PerDay = float64(r.Snooping) / days
	return r
}

func (b *Backend) Status(context.Context) (model.Status, error) {
	now := b.Now()
	st := model.Status{Version: "0.1.0-dev", Demo: b.Demo}
	if b.Empty {
		return st, nil
	}
	st.Devices = len(household())
	st.Oldest, st.Newest = now.AddDate(0, 0, -41), now.Add(-40*time.Second)
	st.Sources = []model.SourceStatus{
		{Name: "pihole", Kind: "dns", LastRun: now.Add(-40 * time.Second), LastOK: now.Add(-40 * time.Second), Records: 1284377},
		{Name: "leases", Kind: "devices", LastRun: now.Add(-2 * time.Minute), LastOK: now.Add(-2 * time.Minute), Records: 10},
	}
	return st, nil
}

func (b *Backend) SetLabel(_ context.Context, id, label string) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.labels == nil {
		b.labels = map[string]string{}
	}
	b.labels[id] = label
	return nil
}

// Receipt returns a placeholder; the real one comes from internal/receipt.
func (b *Backend) Receipt(_ context.Context, p model.Period, id, format string) ([]byte, error) {
	title := "Home"
	for _, d := range household() {
		if d.ID == id {
			title = d.DisplayName()
		}
	}
	if format == "png" {
		img := image.NewGray(image.Rect(0, 0, 576, 800))
		for i := range img.Pix {
			img.Pix[i] = 0xf6
		}
		var buf bytes.Buffer
		err := png.Encode(&buf, img)
		return buf.Bytes(), err
	}
	return fmt.Appendf(nil, `<svg xmlns="http://www.w3.org/2000/svg" width="576" height="800" viewBox="0 0 576 800">`+
		`<rect width="576" height="800" fill="#fffdf8"/>`+
		`<g font-family="monospace" fill="#222" text-anchor="middle">`+
		`<text x="288" y="80" font-size="28" font-weight="bold">PRIVACY RECEIPT</text>`+
		`<text x="288" y="120" font-size="18">%s</text>`+
		`<text x="288" y="150" font-size="14">%s – %s</text>`+
		`<text x="288" y="420" font-size="14" fill="#888">placeholder from the web fixture</text>`+
		`</g></svg>`, html.EscapeString(title), p.From.Format("Jan 2"), p.To.Format("Jan 2")), nil
}
