package main

import (
	"testing"

	"github.com/bizpers11991-code/phonehome/internal/config"
)

func TestNewAlerts(t *testing.T) {
	e, err := newAlerts(config.Default().Alerts, nil, nil)
	if err != nil || e != nil {
		t.Fatalf("no targets must mean no engine: %v, %v", e, err)
	}
	c, err := config.Parse([]byte("alerts:\n  webhook: {url: https://h.lan/x}\n  mqtt: {broker: mqtt://b.lan}\n"))
	if err != nil {
		t.Fatal(err)
	}
	e, err = newAlerts(c.Alerts, nil, nil)
	if err != nil || len(e.Notifiers) != 2 || len(e.Publishers) != 1 || e.MaxPerHour != 6 {
		t.Fatalf("engine %+v, %v", e, err)
	}
	c.Alerts.Gotify = &config.Gotify{URL: "https://g.lan", TokenFile: "/nonexistent/token"}
	if _, err := newAlerts(c.Alerts, nil, nil); err == nil {
		t.Fatal("unreadable token file accepted")
	}
}
