package demo

import (
	"net/netip"
	"time"

	"github.com/bizpers11991-code/phonehome/internal/model"
)

// Hostnames used by the synthetic household and where they come from, so the
// knowledge base can be reconciled with them. The intended category is noted
// in brackets; the KB is the authority, not this file.
//
// [1] Anselmi, Vekaria et al., "Watching TV with the Second-Party: A First
//     Look at ACR Tracking in Smart TVs", IMC 2024, https://arxiv.org/abs/2409.06203
//     (Samsung ≈ once a minute, LG ≈ every 15 s; works even as an HDMI display)
// [2] Perflyst SmartTV / AmazonFireTV blocklists,
//     https://github.com/Perflyst/PiHoleBlocklist
// [3] Moghaddam et al., "Watching You Watch", CCS 2019,
//     https://tv-watches-you.princeton.edu/ (doubleclick.net on 975/1000 Roku channels)
// [4] Vendor documentation / well-known service hostnames.
//
//   Samsung TV  acr-us-prd.samsungcloud.tv [acr,1], log-ingestion.samsungacr.com [acr,1,2],
//               log-config.samsungacr.com [acr,1], acr0.samsungcloudsolution.com [acr,1],
//               ads.samsungads.com [ads,2], config.samsungads.com [ads,2],
//               prderrordumphsm.samsungcloudsolution.com [telemetry,2],
//               osb-ussvc.samsungqbe.com [content,2], time.samsungcloudsolution.com [essential,2],
//               otnprd9.samsungcloudsolution.net [essential: firmware,2], dns.google [DoH,4]
//   LG TV       tkacr1.alphonso.tv [acr,1: "tkacrX"], us.info.lgsmartad.com [ads,2],
//               us.ad.lgsmartad.com [ads,2], us.lgtvsdp.com [essential,2], ngfts.lge.com [content,2],
//               snu.lge.com [essential: firmware,4]
//   Roku        scribe.logs.roku.com, austin.logs.roku.com, midland.logs.roku.com [telemetry,2],
//               pubads.g.doubleclick.net [ads,3], api.roku.com, image.roku.com [content,4]
//   Echo        device-metrics-us.amazon.com [telemetry,2], unagi-na.amazon.com [telemetry,2],
//               avs-alexa-14-na.amazon.com, api.amazonalexa.com [content,4], ntp-g7g.amazon.com [essential,4]
//   Nest Mini   connectivitycheck.gstatic.com, time.google.com [essential,4],
//               play.googleapis.com [telemetry: clearcut logging,4],
//               googlehomefoyer-pa.googleapis.com [content,4]
//   Ring        api.ring.com, fw.ring.com [essential,4], app-snaps.ring.com [content,4]
//   Roborock    usiot.roborock.com, api-us.roborock.com [essential,4]
//   Hue         ws.meethue.com, firmware.meethue.com [essential,4], diag.meethue.com [telemetry,4]
//   iPhone/Mac  Apple, Meta, Google, Spotify, Slack, GitHub service hostnames [4]

// Usage profiles: probability of active use in each local hour.
var (
	tvEvening = &[24]float64{
		0.05, 0.02, 0, 0, 0, 0, 0.05, 0.15, 0.1, 0.05, 0.05, 0.05,
		0.05, 0.05, 0.05, 0.1, 0.15, 0.4, 0.8, 0.95, 0.95, 0.9, 0.6, 0.25,
	}
	tvWeekend = &[24]float64{
		0.1, 0.05, 0, 0, 0, 0, 0, 0.1, 0.4, 0.5, 0.5, 0.4,
		0.4, 0.5, 0.6, 0.6, 0.6, 0.7, 0.9, 0.95, 0.95, 0.9, 0.7, 0.4,
	}
	bedtimeTV = &[24]float64{
		0.15, 0.05, 0, 0, 0, 0, 0, 0.05, 0, 0, 0, 0,
		0, 0, 0, 0, 0, 0, 0, 0.05, 0.2, 0.6, 0.85, 0.5,
	}
	speaker = &[24]float64{
		0, 0, 0, 0, 0, 0, 0.3, 0.8, 0.6, 0.2, 0.1, 0.1,
		0.3, 0.1, 0.1, 0.1, 0.2, 0.5, 0.8, 0.7, 0.4, 0.2, 0.1, 0,
	}
	doorbell = &[24]float64{
		0, 0, 0, 0, 0, 0, 0.1, 0.5, 0.6, 0.3, 0.3, 0.4,
		0.5, 0.4, 0.4, 0.5, 0.6, 0.7, 0.6, 0.4, 0.2, 0.1, 0.05, 0,
	}
	vacuumWeekday = &[24]float64{9: 0.9, 10: 0.6}
	vacuumWeekend = &[24]float64{11: 0.5}
	phone         = &[24]float64{
		0.05, 0, 0, 0, 0, 0, 0.3, 0.8, 0.8, 0.6, 0.5, 0.5,
		0.8, 0.6, 0.5, 0.5, 0.6, 0.8, 0.8, 0.7, 0.7, 0.8, 0.7, 0.3,
	}
	laptop = &[24]float64{
		0, 0, 0, 0, 0, 0, 0, 0.1, 0.5, 0.95, 0.95, 0.95,
		0.7, 0.95, 0.95, 0.95, 0.9, 0.6, 0.2, 0.2, 0.4, 0.4, 0.2, 0.05,
	}
	laptopWeekend = &[24]float64{10: 0.3, 11: 0.3, 14: 0.2, 20: 0.4, 21: 0.4}
	always        = &[24]float64{1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1}
)

var (
	qA      = []string{"A", "A", "A", "A", "A", "AAAA"}
	qApple  = []string{"A", "A", "AAAA", "AAAA", "HTTPS"}
	qGoogle = []string{"A", "A", "AAAA"}
)

func dev(id int, mac, host, label, vendor string, kind model.DeviceKind) model.Device {
	return model.Device{
		ID:       "mac:" + mac,
		MAC:      mac,
		IPs:      []netip.Addr{netip.AddrFrom4([4]byte{192, 168, 1, byte(id)})},
		Hostname: host,
		Vendor:   vendor,
		Kind:     kind,
		Label:    label,
	}
}

// household is the cast. MAC prefixes are real IEEE OUIs for each vendor; the
// rest of each address is made up. The iPhone uses a randomized
// (locally administered) MAC, as iOS does by default.
func household() []spec {
	const m, s = time.Minute, time.Second
	return []spec{
		{
			// The villain: ACR on a steady clock around the clock, even overnight.
			device: dev(20, "8c:b0:e9:4a:17:c2", "Samsung-QN65Q80", "Living Room TV", "Samsung", model.KindTV),
			usage:  tvEvening, weekend: tvWeekend, perHour: 420, qtypes: qA,
			use: []dom{
				{"ads.samsungads.com", 10, false},
				{"config.samsungads.com", 4, false},
				{"prderrordumphsm.samsungcloudsolution.com", 3, false},
				{"osb-ussvc.samsungqbe.com", 8, false},
				{"api-global.netflix.com", 12, false},
				{"occ-0-2794-2219.1.nflxso.net", 10, false},
				{"www.youtube.com", 8, false},
				{"i.ytimg.com", 8, false},
				{"rr4---sn-a5mekn6s.googlevideo.com", 10, false},
			},
			beats: []beat{
				{name: "acr-us-prd.samsungcloud.tv", every: 60 * s, jitter: 0.04, always: true},
				{name: "log-ingestion.samsungacr.com", every: 5 * m, jitter: 0.05, always: true},
				{name: "log-config.samsungacr.com", every: 30 * m, jitter: 0.05, always: true},
				{name: "acr0.samsungcloudsolution.com", every: 15 * m, jitter: 0.05, always: true},
				{name: "time.samsungcloudsolution.com", every: time.Hour, jitter: 0.1, always: true},
				{name: "otnprd9.samsungcloudsolution.net", every: 12 * time.Hour, jitter: 0.2, always: true},
				{name: "dns.google", every: 6 * time.Hour, jitter: 0.3, always: true, doh: true},
			},
		},
		{
			// ACR too, but only while it is switched on.
			device: dev(21, "3c:bd:d8:91:05:6e", "LGwebOSTV", "Bedroom TV", "LG", model.KindTV),
			usage:  bedtimeTV, perHour: 220, qtypes: qA,
			use: []dom{
				{"us.info.lgsmartad.com", 6, false},
				{"us.ad.lgsmartad.com", 6, false},
				{"us.lgtvsdp.com", 6, false},
				{"ngfts.lge.com", 5, false},
				{"api-global.netflix.com", 10, false},
				{"occ-0-2794-2219.1.nflxso.net", 10, false},
				{"www.youtube.com", 6, false},
				{"rr2---sn-a5meknzr.googlevideo.com", 8, false},
			},
			beats: []beat{
				{name: "tkacr1.alphonso.tv", every: 30 * s, jitter: 0.08},
				{name: "us.lgtvsdp.com", every: time.Hour, jitter: 0.1, always: true},
				{name: "snu.lge.com", every: 12 * time.Hour, jitter: 0.2, always: true},
			},
		},
		{
			device: dev(22, "88:de:a9:3b:c0:51", "Roku-Streaming-Stick", "Roku Streaming Stick", "Roku", model.KindStreamer),
			usage:  tvEvening, weekend: tvWeekend, perHour: 260, qtypes: qA,
			use: []dom{
				{"api.roku.com", 10, false},
				{"image.roku.com", 10, false},
				{"pubads.g.doubleclick.net", 8, true},
				{"austin.logs.roku.com", 6, false},
				{"midland.logs.roku.com", 4, false},
				{"www.hulu.com", 8, false},
				{"manifest-dp.hulustream.com", 6, false},
			},
			beats: []beat{
				{name: "scribe.logs.roku.com", every: 2 * m, jitter: 0.1, always: true},
			},
		},
		{
			device: dev(30, "74:c2:46:e0:8d:13", "amazon-7c1a0e9b2", "Kitchen Echo", "Amazon", model.KindSpeaker),
			usage:  speaker, perHour: 120, qtypes: qA,
			use: []dom{
				{"api.amazonalexa.com", 10, false},
				{"avs-alexa-14-na.amazon.com", 6, false},
				{"dmls-na.amazon.com", 4, false},
				{"unagi-na.amazon.com", 3, false},
			},
			beats: []beat{
				{name: "avs-alexa-14-na.amazon.com", every: 2 * m, jitter: 0.1, always: true},
				{name: "device-metrics-us.amazon.com", every: 5 * m, jitter: 0.1, always: true, blocked: true},
				{name: "unagi-na.amazon.com", every: 10 * m, jitter: 0.1, always: true},
				{name: "ntp-g7g.amazon.com", every: time.Hour, jitter: 0.05, always: true},
			},
		},
		{
			device: dev(31, "1c:f2:9a:62:4b:e8", "Google-Nest-Mini", "Nest Mini", "Google", model.KindSpeaker),
			usage:  speaker, perHour: 60, qtypes: qGoogle,
			use: []dom{
				{"googlehomefoyer-pa.googleapis.com", 10, false},
				{"www.google.com", 6, false},
				{"play.googleapis.com", 2, false},
			},
			beats: []beat{
				{name: "connectivitycheck.gstatic.com", every: 5 * m, jitter: 0.05, always: true},
				{name: "play.googleapis.com", every: 15 * m, jitter: 0.2, always: true},
				{name: "time.google.com", every: time.Hour, jitter: 0.05, always: true},
			},
		},
		{
			device: dev(40, "18:7f:88:c4:2d:90", "RingDoorbell-90", "Front Door", "Ring", model.KindCamera),
			usage:  doorbell, perHour: 40, qtypes: qA,
			use: []dom{
				{"app-snaps.ring.com", 10, false},
				{"api.ring.com", 5, false},
			},
			beats: []beat{
				{name: "api.ring.com", every: 2 * m, jitter: 0.1, always: true},
				{name: "fw.ring.com", every: time.Hour, jitter: 0.1, always: true},
			},
		},
		{
			device: dev(41, "b0:4a:39:7e:11:a5", "roborock-vacuum-a15", "Roborock", "Roborock", model.KindVacuum),
			usage:  vacuumWeekday, weekend: vacuumWeekend, perHour: 90, qtypes: qA,
			use: []dom{
				{"api-us.roborock.com", 10, false},
				{"usiot.roborock.com", 5, false},
			},
			beats: []beat{
				{name: "usiot.roborock.com", every: 5 * m, jitter: 0.1, always: true},
				{name: "pool.ntp.org", every: time.Hour, jitter: 0.1, always: true},
			},
		},
		{
			// The good citizen: a few essential lookups, a sliver of diagnostics.
			device: dev(42, "ec:b5:fa:0d:33:7c", "Philips-hue", "Hue Bridge", "Philips Hue", model.KindHub),
			usage:  always, perHour: 0, qtypes: qA,
			beats: []beat{
				{name: "ws.meethue.com", every: 30 * m, jitter: 0.1, always: true},
				{name: "firmware.meethue.com", every: 6 * time.Hour, jitter: 0.2, always: true},
				{name: "diag.meethue.com", every: 24 * time.Hour, jitter: 0.2, always: true},
			},
		},
		{
			device: dev(50, "a6:3f:91:0c:5e:27", "iPhone", "iPhone", "", model.KindPhone),
			usage:  phone, perHour: 260, qtypes: qApple,
			use: []dom{
				{"gateway.icloud.com", 8, false},
				{"mask.icloud.com", 6, false},
				{"xp.apple.com", 4, false},
				{"i.instagram.com", 10, false},
				{"scontent.cdninstagram.com", 12, false},
				{"graph.facebook.com", 5, false},
				{"g.whatsapp.net", 6, false},
				{"spclient.wg.spotify.com", 6, false},
				{"app-measurement.com", 4, false},
				{"firebaselogging-pa.googleapis.com", 3, false},
				{"googleads.g.doubleclick.net", 3, true},
				{"settings.crashlytics.com", 2, false},
				{"weatherkit.apple.com", 3, false},
			},
			beats: []beat{
				{name: "captive.apple.com", every: 30 * m, jitter: 0.3, always: true},
				{name: "time.apple.com", every: 4 * time.Hour, jitter: 0.1, always: true},
			},
		},
		{
			device: dev(51, "3c:22:fb:b8:46:0a", "MacBook-Pro", "MacBook", "Apple", model.KindComputer),
			usage:  laptop, weekend: laptopWeekend, perHour: 320, qtypes: qApple,
			use: []dom{
				{"github.com", 10, false},
				{"api.github.com", 6, false},
				{"edgeapi.slack.com", 8, false},
				{"wss-primary.slack.com", 4, false},
				{"www.google.com", 8, false},
				{"fonts.googleapis.com", 4, false},
				{"www.googletagmanager.com", 3, false},
				{"www.google-analytics.com", 3, true},
				{"connect.facebook.net", 2, false},
				{"www.youtube.com", 4, false},
				{"zoom.us", 3, false},
				{"xp.apple.com", 2, false},
				{"ocsp.apple.com", 3, false},
			},
			beats: []beat{
				{name: "swscan.apple.com", every: 6 * time.Hour, jitter: 0.2, always: true},
				{name: "time.apple.com", every: time.Hour, jitter: 0.1, always: true},
			},
		},
	}
}
