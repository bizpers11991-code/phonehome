package config

import (
	"strings"
	"testing"
	"time"
)

func TestAlertsConfig(t *testing.T) {
	c, err := Parse([]byte(`
alerts:
  events: [new_device, bypass]
  interval: 10m
  webhook:
    url: https://hooks.example.org/phonehome
    headers: {Authorization: "Bearer x"}
  ntfy:
    url: https://ntfy.sh/my-phonehome
    token: tk_abc
  gotify:
    url: https://gotify.lan
    token: AbCd
  mqtt:
    broker: mqtt://192.168.1.2:1883
    username: phonehome
    password: pw
`))
	if err != nil {
		t.Fatal(err)
	}
	a := c.Alerts
	if !a.Enabled() || a.Interval != 10*time.Minute || a.MaxPerHour != 6 || !a.Wants(EventBypass) || a.Wants(EventHeartbeat) {
		t.Fatalf("alerts %+v", a)
	}
	if m := a.MQTT; m.ClientID != "phonehome" || m.Topic != "phonehome" || !*m.Discovery || m.DiscoveryPfx != "homeassistant" {
		t.Fatalf("mqtt defaults %+v", m)
	}
	if d, err := Parse(nil); err != nil || d.Alerts.Enabled() || !d.Alerts.Wants(EventGradeWorse) {
		t.Fatalf("alerts must be off by default: %+v %v", d.Alerts, err)
	}
	if c, err := Parse([]byte("alerts:\n  mqtt:\n    broker: mqtts://b.lan\n    discovery: false\n")); err != nil || *c.Alerts.MQTT.Discovery {
		t.Fatalf("discovery: false ignored: %v", err)
	}
}

func TestAlertsConfigErrors(t *testing.T) {
	for yaml, want := range map[string]string{
		"alerts:\n  events: [everything]\n":                                  `unknown event "everything"`,
		"alerts:\n  interval: 10s\n":                                         "too short",
		"alerts:\n  max_per_hour: -1\n":                                      "max_per_hour",
		"alerts:\n  webhook: {url: ftp://x}\n":                               "webhook.url",
		"alerts:\n  ntfy: {url: https://ntfy.sh}\n":                          "topic's full address",
		"alerts:\n  ntfy: {url: https://ntfy.sh/t, token: a, username: u}\n": "not both",
		"alerts:\n  ntfy: {url: https://ntfy.sh/t, password: pw}\n":          "needs a username",
		"alerts:\n  gotify: {url: https://g.lan}\n":                          "application token",
		"alerts:\n  mqtt: {broker: http://b.lan}\n":                          "mqtt.broker",
		"alerts:\n  mqtt: {broker: mqtt://b.lan, password: x}\n":             "needs a username",
		"alerts:\n  mqtt: {broker: mqtt://b.lan, topic: home/#}\n":           "wildcards",
	} {
		_, err := Parse([]byte(yaml))
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%q: err = %v, want %q", yaml, err, want)
		}
	}
}
