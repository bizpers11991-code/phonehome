package analyze

import (
	"cmp"
	"regexp"
	"strings"

	"github.com/bizpers11991-code/phonehome/internal/model"
)

// Domain-evidence thresholds for InferKind: the dominant hinted kind must
// account for at least half of all hinted lookups, and at least
// strongHintMin of them to override what the device says about itself.
const (
	strongHintMin   = 5
	dominantHintPct = 50
)

type kindRule struct {
	kind model.DeviceKind
	re   *regexp.Regexp
}

// nameRules match a lowercased label or hostname. Order matters: the first
// match wins, so specific names come before the generic tokens they contain
// ("fire-tv" is a streamer before "tv" makes it a TV; "nest-hub" a speaker).
// Short tokens are anchored on non-letters so "spring" is not a Ring camera.
var nameRules = []kindRule{
	{model.KindStreamer, regexp.MustCompile(`roku|fire-?tv|chromecast|apple-?tv|shield`)},
	{model.KindConsole, regexp.MustCompile(`xbox|playstation|nintendo|(^|[^a-z])ps[45]([^0-9]|$)|(^|[^a-z])switch([^a-z]|$)`)},
	{model.KindTV, regexp.MustCompile(`bravia|tizen|webos|vizio|(^|[^a-z])tv|tv($|[^a-z])`)},
	{model.KindPhone, regexp.MustCompile(`iphone|ipad|android|pixel|galaxy|oneplus`)},
	{model.KindComputer, regexp.MustCompile(`macbook|laptop|desktop|imac|thinkpad|mac-?mini|(^|[^a-z])pc([^a-z]|$)`)},
	{model.KindSpeaker, regexp.MustCompile(`echo|alexa|nest-?(mini|audio|hub)|homepod|sonos|google-?home`)},
	{model.KindCamera, regexp.MustCompile(`doorbell|arlo|wyze|eufy|blink|(^|[^a-z])ring([^a-z]|$)|cam(era)?([^a-z]|$)`)},
	{model.KindVacuum, regexp.MustCompile(`roborock|roomba|irobot|vacuum|deebot|dreame`)},
	{model.KindHub, regexp.MustCompile(`philips-?hue|(^|[^a-z])hue([^a-z]|$)|smartthings|hubitat`)},
	{model.KindPlug, regexp.MustCompile(`plug|bulb|kasa|tapo|shelly|meross|lifx|wemo`)},
}

// vendorRules match a lowercased MAC/OUI vendor. Only vendors that make one
// kind of consumer device are listed: "Samsung" or "Amazon" could be anything.
var vendorRules = []kindRule{
	{model.KindStreamer, regexp.MustCompile(`roku`)},
	{model.KindTV, regexp.MustCompile(`vizio|hisense|tcl`)},
	{model.KindSpeaker, regexp.MustCompile(`sonos`)},
	{model.KindCamera, regexp.MustCompile(`(^|[^a-z])ring([^a-z]|$)|arlo|wyze|eufy|reolink|hikvision|dahua`)},
	{model.KindVacuum, regexp.MustCompile(`irobot|roborock|ecovacs|dreame`)},
	{model.KindHub, regexp.MustCompile(`signify|philips lighting`)},
	{model.KindPlug, regexp.MustCompile(`shelly|meross|lifx|belkin`)},
	{model.KindConsole, regexp.MustCompile(`nintendo|sony interactive`)},
	{model.KindNetwork, regexp.MustCompile(`ubiquiti|netgear|synology|qnap|mikrotik`)},
}

func matchKind(rules []kindRule, s string) model.DeviceKind {
	if s == "" {
		return ""
	}
	s = strings.ToLower(s)
	for _, r := range rules {
		if r.re.MatchString(s) {
			return r.kind
		}
	}
	return ""
}

// InferKind makes a coarse guess at what d is. hints counts lookups whose
// knowledge-base rule implies a kind (Classification.KindHint).
//
// What a device does is better evidence than what it is called, so the
// sources are consulted in this order and the first that answers wins:
//
//  1. strong domain evidence: one hinted kind has ≥ 5 lookups and ≥ 50% of
//     all hinted lookups (a box that talks to Samsung's ACR servers all day
//     is a TV, whatever DHCP calls it);
//  2. d.Kind, when a source or the user already set one other than unknown;
//  3. the user's label, then the hostname, matched against name patterns;
//  4. the MAC vendor, for single-purpose vendors only;
//  5. weak domain evidence: a dominant hinted kind with fewer lookups;
//  6. model.KindUnknown.
func InferKind(d model.Device, hints map[model.DeviceKind]int) model.DeviceKind {
	dominant, n, total := dominantHint(hints)
	majority := n > 0 && n*100 >= total*dominantHintPct
	if majority && n >= strongHintMin {
		return dominant
	}
	if d.Kind != "" && d.Kind != model.KindUnknown {
		return d.Kind
	}
	for _, k := range []model.DeviceKind{
		matchKind(nameRules, d.Label),
		matchKind(nameRules, d.Hostname),
		matchKind(vendorRules, d.Vendor),
	} {
		if k != "" {
			return k
		}
	}
	if majority {
		return dominant
	}
	return model.KindUnknown
}

// dominantHint returns the most-hinted kind (ties broken by name so the result
// is stable), its count, and the total of all hints.
func dominantHint(hints map[model.DeviceKind]int) (kind model.DeviceKind, n, total int) {
	for k, c := range hints {
		if k == "" || k == model.KindUnknown || c <= 0 {
			continue
		}
		total += c
		if c > n || (c == n && cmp.Less(k, kind)) {
			kind, n = k, c
		}
	}
	return kind, n, total
}
