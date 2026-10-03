package main

import (
	"context"
	"fmt"

	"github.com/bizpers11991-code/phonehome/internal/alert"
	"github.com/bizpers11991-code/phonehome/internal/config"
	"github.com/bizpers11991-code/phonehome/internal/store"
)

// newAlerts builds the alert engine from the config's targets, or returns
// nil when none is configured: phonehome then contacts nothing for alerts.
func newAlerts(cfg config.Alerts, a *app, st *store.Store) (*alert.Engine, error) {
	if !cfg.Enabled() {
		return nil, nil
	}
	e := &alert.Engine{Report: a.Report, Store: st, Wants: cfg.Wants, MaxPerHour: cfg.MaxPerHour, Logger: logger}
	if w := cfg.Webhook; w != nil {
		e.Notifiers = append(e.Notifiers, &alert.Webhook{URL: w.URL, Headers: w.Headers})
	}
	if n := cfg.Ntfy; n != nil {
		token, pass, err := n.Secret()
		if err != nil {
			return nil, fmt.Errorf("alerts.ntfy: %w", err)
		}
		e.Notifiers = append(e.Notifiers, &alert.Ntfy{URL: n.URL, Token: token, Username: n.Username, Password: pass, Priority: n.Priority})
	}
	if g := cfg.Gotify; g != nil {
		token, err := g.Secret()
		if err != nil {
			return nil, fmt.Errorf("alerts.gotify: %w", err)
		}
		e.Notifiers = append(e.Notifiers, &alert.Gotify{URL: g.URL, Token: token, Priority: g.Priority})
	}
	if m := cfg.MQTT; m != nil {
		pass, err := m.Secret()
		if err != nil {
			return nil, fmt.Errorf("alerts.mqtt: %w", err)
		}
		mq := &alert.MQTT{Broker: m.Broker, Username: m.Username, Password: pass, ClientID: m.ClientID, Topic: m.Topic,
			Discovery: *m.Discovery, DiscoveryPrefix: m.DiscoveryPfx, Version: version}
		e.Notifiers = append(e.Notifiers, mq)
		e.Publishers = append(e.Publishers, mq)
	}
	return e, nil
}

// runAlerts checks for alerts every interval until ctx ends.
func runAlerts(ctx context.Context, cfg config.Alerts, a *app, st *store.Store) error {
	e, err := newAlerts(cfg, a, st)
	if err != nil || e == nil {
		return err
	}
	logger.Info("alerts on", "targets", len(e.Notifiers), "every", cfg.Interval)
	go e.Run(ctx, cfg.Interval)
	return nil
}
