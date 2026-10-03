package config

import (
	"errors"
	"fmt"
	"net/url"
	"slices"
	"strings"
	"time"
)

// Alert events phonehome can notify about.
const (
	EventNewDevice  = "new_device"
	EventGradeWorse = "grade_worse"
	EventHeartbeat  = "heartbeat"
	EventBypass     = "bypass"
)

var alertEvents = []string{EventNewDevice, EventGradeWorse, EventHeartbeat, EventBypass}

// Alerts sends notifications to services the user runs. Nothing is sent
// unless at least one target is configured.
type Alerts struct {
	Events     []string      `yaml:"events"`       // empty = all
	Interval   time.Duration `yaml:"interval"`     // how often to check; default 5m
	MaxPerHour int           `yaml:"max_per_hour"` // notifications per hour, per check batch; default 6
	Webhook    *Webhook      `yaml:"webhook"`
	Ntfy       *Ntfy         `yaml:"ntfy"`
	Gotify     *Gotify       `yaml:"gotify"`
	MQTT       *MQTT         `yaml:"mqtt"`
}

// Webhook POSTs every alert batch as JSON to URL.
type Webhook struct {
	URL     string            `yaml:"url"`
	Headers map[string]string `yaml:"headers"`
}

// Ntfy publishes to an ntfy topic: URL is the topic's full address, such as
// https://ntfy.sh/my-phonehome-alerts.
type Ntfy struct {
	URL          string `yaml:"url"`
	Token        string `yaml:"token"`
	TokenFile    string `yaml:"token_file"`
	Username     string `yaml:"username"`
	Password     string `yaml:"password"`
	PasswordFile string `yaml:"password_file"`
	Priority     string `yaml:"priority"` // ntfy priority name or 1–5; default ntfy's
}

// Gotify posts to a Gotify server with an application token.
type Gotify struct {
	URL       string `yaml:"url"`
	Token     string `yaml:"token"`
	TokenFile string `yaml:"token_file"`
	Priority  int    `yaml:"priority"`
}

// MQTT publishes alerts and per-device grades to a broker.
type MQTT struct {
	Broker       string `yaml:"broker"` // mqtt://host:1883 or mqtts://host:8883
	Username     string `yaml:"username"`
	Password     string `yaml:"password"`
	PasswordFile string `yaml:"password_file"`
	ClientID     string `yaml:"client_id"`        // default "phonehome"
	Topic        string `yaml:"topic"`            // topic prefix, default "phonehome"
	Discovery    *bool  `yaml:"discovery"`        // Home Assistant discovery; default true
	DiscoveryPfx string `yaml:"discovery_prefix"` // default "homeassistant"
}

// Enabled reports whether any target is configured.
func (a Alerts) Enabled() bool {
	return a.Webhook != nil || a.Ntfy != nil || a.Gotify != nil || a.MQTT != nil
}

// Wants reports whether the event kind should be sent.
func (a Alerts) Wants(event string) bool {
	return len(a.Events) == 0 || slices.Contains(a.Events, event)
}

func (a *Alerts) fillDefaults() {
	if m := a.MQTT; m != nil {
		if m.ClientID == "" {
			m.ClientID = "phonehome"
		}
		if m.Topic == "" {
			m.Topic = "phonehome"
		}
		if m.Discovery == nil {
			on := true
			m.Discovery = &on
		}
		if m.DiscoveryPfx == "" {
			m.DiscoveryPfx = "homeassistant"
		}
	}
}

func (a Alerts) validate() error {
	var errs []error
	bad := func(format string, args ...any) { errs = append(errs, fmt.Errorf("alerts: "+format, args...)) }
	for _, e := range a.Events {
		if !slices.Contains(alertEvents, e) {
			bad("unknown event %q; use any of %s", e, strings.Join(alertEvents, ", "))
		}
	}
	if a.Interval < time.Minute {
		bad(`interval %v is too short; use at least "1m"`, a.Interval)
	}
	if a.MaxPerHour < 1 {
		bad("max_per_hour is %d; use a positive number", a.MaxPerHour)
	}
	if w := a.Webhook; w != nil && !httpURL(w.URL) {
		bad(`webhook.url %q must be an http:// or https:// address`, w.URL)
	}
	if n := a.Ntfy; n != nil {
		u, err := url.Parse(n.URL)
		if !httpURL(n.URL) || err != nil || strings.Trim(u.Path, "/") == "" {
			bad(`ntfy.url %q must be the topic's full address, e.g. https://ntfy.sh/my-topic`, n.URL)
		}
		if n.Token != "" && n.TokenFile != "" || n.Password != "" && n.PasswordFile != "" {
			bad("ntfy: set either token or token_file, and either password or password_file, not both")
		}
		if (n.Token != "" || n.TokenFile != "") && n.Username != "" {
			bad("ntfy: use a token or a username and password, not both")
		}
		if (n.Password != "" || n.PasswordFile != "") && n.Username == "" {
			bad("ntfy: a password needs a username; or use an access token")
		}
	}
	if g := a.Gotify; g != nil {
		if !httpURL(g.URL) {
			bad(`gotify.url %q must be the server's address, e.g. https://gotify.example.org`, g.URL)
		}
		if (g.Token == "") == (g.TokenFile == "") {
			bad("gotify: set the application token in token or token_file (one of them)")
		}
	}
	if m := a.MQTT; m != nil {
		u, err := url.Parse(m.Broker)
		if err != nil || u.Host == "" || !slices.Contains([]string{"mqtt", "tcp", "mqtts", "ssl", "tls"}, u.Scheme) {
			bad(`mqtt.broker %q must look like mqtt://192.168.1.2:1883 or mqtts://broker.lan:8883`, m.Broker)
		}
		if m.Password != "" && m.PasswordFile != "" {
			bad("mqtt: set either password or password_file, not both")
		}
		if (m.Password != "" || m.PasswordFile != "") && m.Username == "" {
			bad("mqtt: a password needs a username (MQTT 3.1.1 cannot send one without the other)")
		}
		if strings.ContainsAny(m.Topic+m.DiscoveryPfx, "#+") {
			bad("mqtt: topic and discovery_prefix must not contain the wildcards # or +")
		}
	}
	return errors.Join(errs...)
}

func httpURL(s string) bool {
	u, err := url.Parse(s)
	return err == nil && (u.Scheme == "http" || u.Scheme == "https") && u.Host != ""
}

// Secret returns the ntfy token or password, reading the *_file settings.
func (n Ntfy) Secret() (token, password string, err error) {
	if token, err = secret(n.Token, n.TokenFile); err != nil {
		return "", "", err
	}
	password, err = secret(n.Password, n.PasswordFile)
	return token, password, err
}

// Secret returns the Gotify application token, reading TokenFile if set.
func (g Gotify) Secret() (string, error) { return secret(g.Token, g.TokenFile) }

// Secret returns the MQTT password, reading PasswordFile if set.
func (m MQTT) Secret() (string, error) { return secret(m.Password, m.PasswordFile) }
