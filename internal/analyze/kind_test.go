package analyze

import (
	"testing"

	"github.com/bizpers11991-code/phonehome/internal/model"
)

func TestInferKindPrecedence(t *testing.T) {
	type hints = map[model.DeviceKind]int
	tests := []struct {
		name  string
		dev   model.Device
		hints hints
		want  model.DeviceKind
	}{
		{"nothing known", model.Device{}, nil, model.KindUnknown},
		{"strong evidence beats provided kind", model.Device{Kind: model.KindComputer}, hints{model.KindTV: 5}, model.KindTV},
		{"strong evidence beats hostname", model.Device{Hostname: "Office-PC"}, hints{model.KindTV: 40, model.KindStreamer: 10}, model.KindTV},
		{"exactly half is dominant", model.Device{Hostname: "Office-PC"}, hints{model.KindTV: 6, model.KindPhone: 6}, model.KindPhone},
		{"split evidence is not strong", model.Device{Hostname: "Office-PC"}, hints{model.KindTV: 6, model.KindPhone: 5, model.KindCamera: 2}, model.KindComputer},
		{"too few hits is not strong", model.Device{Hostname: "Office-PC"}, hints{model.KindTV: 4}, model.KindComputer},
		{"provided kind beats hostname", model.Device{Kind: model.KindHub, Hostname: "kasa-plug"}, nil, model.KindHub},
		{"unknown kind is not provided", model.Device{Kind: model.KindUnknown, Hostname: "kasa-plug"}, nil, model.KindPlug},
		{"label beats hostname", model.Device{Label: "Kitchen Echo", Hostname: "android-1a2b"}, nil, model.KindSpeaker},
		{"hostname beats vendor", model.Device{Hostname: "roomba-123", Vendor: "Roku, Inc."}, nil, model.KindVacuum},
		{"vendor when no name matches", model.Device{Hostname: "esp-12ab", Vendor: "Sonos, Inc."}, nil, model.KindSpeaker},
		{"generic vendor stays unknown", model.Device{Vendor: "Samsung Electronics"}, nil, model.KindUnknown},
		{"weak evidence as a last resort", model.Device{Vendor: "Espressif"}, hints{model.KindCamera: 2}, model.KindCamera},
		{"weak minority evidence ignored", model.Device{}, hints{model.KindCamera: 1, model.KindTV: 1, model.KindPlug: 1}, model.KindUnknown},
		{"unknown hints ignored", model.Device{}, hints{model.KindUnknown: 50, "": 9}, model.KindUnknown},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := InferKind(tt.dev, tt.hints); got != tt.want {
				t.Errorf("InferKind = %s, want %s", got, tt.want)
			}
		})
	}
}

func TestNameRules(t *testing.T) {
	tests := map[string]model.DeviceKind{
		"Samsung-TV":         model.KindTV,
		"LGwebOSTV":          model.KindTV,
		"BRAVIA-4K-GB":       model.KindTV,
		"Tizen":              model.KindTV,
		"smarttv":            model.KindTV,
		"Fire-TV-Stick":      model.KindStreamer,
		"FireTV":             model.KindStreamer,
		"Apple-TV":           model.KindStreamer,
		"Roku-Ultra":         model.KindStreamer,
		"Chromecast-Ultra":   model.KindStreamer,
		"SHIELD":             model.KindStreamer,
		"Living-Room-Echo":   model.KindSpeaker,
		"Nest-Mini":          model.KindSpeaker,
		"nest-hub-max":       model.KindSpeaker,
		"HomePod":            model.KindSpeaker,
		"Sonos-Kitchen":      model.KindSpeaker,
		"RingDoorbell-12":    model.KindCamera,
		"ring-cam":           model.KindCamera,
		"Front-Camera":       model.KindCamera,
		"WYZE_CAKP2JFUS":     model.KindCamera,
		"spring-garden":      "", // not a Ring
		"camilas-iphone":     model.KindPhone,
		"Pixel-8":            model.KindPhone,
		"Galaxy-S24":         model.KindPhone,
		"roborock-vacuum-a1": model.KindVacuum,
		"Roomba-i7":          model.KindVacuum,
		"kasa-plug-1":        model.KindPlug,
		"tapo-bulb":          model.KindPlug,
		"shellyplug-s-ab12":  model.KindPlug,
		"Philips-hue":        model.KindHub,
		"MacBook-Pro":        model.KindComputer,
		"DESKTOP-7Q1K2L":     model.KindComputer,
		"office-pc":          model.KindComputer,
		"epcot":              "", // "pc" only as a word
		"XboxOne":            model.KindConsole,
		"PS5-123":            model.KindConsole,
		"Nintendo-Switch":    model.KindConsole,
		"printer":            "",
	}
	for name, want := range tests {
		if got := matchKind(nameRules, name); got != want {
			t.Errorf("matchKind(%q) = %q, want %q", name, got, want)
		}
	}
}
