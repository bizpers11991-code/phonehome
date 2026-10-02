package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/bizpers11991-code/phonehome/internal/model"
)

// UpsertDevices merges ds into the stored devices by ID, in one transaction.
//
// For MAC, Hostname, Vendor and Kind a non-empty incoming value (Kind
// "unknown" counts as empty) replaces the stored one only if the stored one
// is empty or the incoming LastSeen is newer. Label is never touched.
// FirstSeen keeps the earliest and LastSeen the latest known time. Each IP is
// assigned to the device that claimed it most recently, so an address handed
// to a new device by DHCP moves to that device.
func (s *Store) UpsertDevices(ctx context.Context, ds []model.Device) error {
	err := s.write(ctx, func(tx *sql.Tx) error {
		for _, in := range ds {
			if in.ID == "" {
				return errors.New("device with empty ID")
			}
			old, err := loadDevice(ctx, tx, in.ID)
			if err != nil {
				return err
			}
			d := mergeDevice(old, in)
			if _, err := tx.ExecContext(ctx, `
				INSERT INTO devices(id, mac, hostname, vendor, kind, label, first_seen, last_seen)
				VALUES (?, ?, ?, ?, ?, ?, ?, ?)
				ON CONFLICT(id) DO UPDATE SET
					mac = excluded.mac, hostname = excluded.hostname, vendor = excluded.vendor,
					kind = excluded.kind, first_seen = excluded.first_seen, last_seen = excluded.last_seen`,
				d.ID, d.MAC, d.Hostname, d.Vendor, string(d.Kind), d.Label, nanos(d.FirstSeen), nanos(d.LastSeen)); err != nil {
				return err
			}
			for _, ip := range in.IPs {
				if !ip.IsValid() {
					continue
				}
				if _, err := tx.ExecContext(ctx, `
					INSERT INTO device_ips(ip, device_id, last_seen) VALUES (?, ?, ?)
					ON CONFLICT(ip) DO UPDATE SET device_id = excluded.device_id, last_seen = excluded.last_seen
					WHERE excluded.last_seen >= device_ips.last_seen`,
					ipBytes(ip), in.ID, nanos(in.LastSeen)); err != nil {
					return err
				}
			}
		}
		return nil
	})
	if err != nil {
		return fmt.Errorf("store: upsert devices: %w", err)
	}
	return nil
}

// loadDevice returns the stored device (without IPs), or a Device with only
// ID set if there is none.
func loadDevice(ctx context.Context, tx *sql.Tx, id string) (model.Device, error) {
	d := model.Device{ID: id}
	var kind string
	var first, last int64
	err := tx.QueryRowContext(ctx,
		`SELECT mac, hostname, vendor, kind, label, first_seen, last_seen FROM devices WHERE id = ?`, id).
		Scan(&d.MAC, &d.Hostname, &d.Vendor, &kind, &d.Label, &first, &last)
	if err == sql.ErrNoRows {
		return d, nil
	}
	d.Kind, d.FirstSeen, d.LastSeen = model.DeviceKind(kind), fromNanos(first), fromNanos(last)
	return d, err
}

// mergeDevice applies the UpsertDevices rules to the stored device old and
// the incoming observation in. IPs are handled separately.
func mergeDevice(old, in model.Device) model.Device {
	newer := in.LastSeen.After(old.LastSeen)
	pick := func(stored, incoming string) string {
		if incoming != "" && (stored == "" || newer) {
			return incoming
		}
		return stored
	}
	d := old
	d.IPs = nil
	d.MAC = pick(old.MAC, in.MAC)
	d.Hostname = pick(old.Hostname, in.Hostname)
	d.Vendor = pick(old.Vendor, in.Vendor)
	known := func(k model.DeviceKind) string {
		if k == model.KindUnknown {
			return ""
		}
		return string(k)
	}
	if k := pick(known(old.Kind), known(in.Kind)); k != "" {
		d.Kind = model.DeviceKind(k)
	}
	d.FirstSeen = earliest(old.FirstSeen, in.FirstSeen)
	if newer {
		d.LastSeen = in.LastSeen
	}
	return d
}

// earliest returns the earlier of a and b, ignoring zero times.
func earliest(a, b time.Time) time.Time {
	if a.IsZero() || (!b.IsZero() && b.Before(a)) {
		return b
	}
	return a
}

// SetLabel sets the user's name for a device, creating the device if it has
// not been seen yet (labels from config can arrive first). An empty label
// clears it.
func (s *Store) SetLabel(ctx context.Context, deviceID, label string) error {
	if deviceID == "" {
		return errors.New("store: set label: empty device ID")
	}
	err := s.write(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `INSERT INTO devices(id, label) VALUES (?, ?)
			ON CONFLICT(id) DO UPDATE SET label = excluded.label`, deviceID, label)
		return err
	})
	if err != nil {
		return fmt.Errorf("store: set label: %w", err)
	}
	return nil
}

// Devices returns every known device ordered by ID, each with its IPs
// ordered most recently seen first.
func (s *Store) Devices(ctx context.Context) ([]model.Device, error) {
	// One statement, so devices and IPs come from the same snapshot.
	rows, err := s.db.QueryContext(ctx, `
		SELECT d.id, d.mac, d.hostname, d.vendor, d.kind, d.label, d.first_seen, d.last_seen, i.ip
		FROM devices d LEFT JOIN device_ips i ON i.device_id = d.id
		ORDER BY d.id, i.last_seen DESC, i.ip`)
	if err != nil {
		return nil, fmt.Errorf("store: devices: %w", err)
	}
	defer rows.Close()
	var out []model.Device
	for rows.Next() {
		var (
			d           model.Device
			kind        string
			first, last int64
			ip          []byte
		)
		if err := rows.Scan(&d.ID, &d.MAC, &d.Hostname, &d.Vendor, &kind, &d.Label, &first, &last, &ip); err != nil {
			return nil, fmt.Errorf("store: devices: %w", err)
		}
		if n := len(out); n == 0 || out[n-1].ID != d.ID {
			d.Kind, d.FirstSeen, d.LastSeen = model.DeviceKind(kind), fromNanos(first), fromNanos(last)
			out = append(out, d)
		}
		if ip != nil {
			cur := &out[len(out)-1]
			cur.IPs = append(cur.IPs, ipFrom(ip))
		}
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store: devices: %w", err)
	}
	return out, nil
}
