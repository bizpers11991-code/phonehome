package store

import (
	"context"
	"database/sql"
	"fmt"
)

// AlertState returns the value the alert engine saved under key, or "" if
// there is none.
func (s *Store) AlertState(ctx context.Context, key string) (string, error) {
	var v string
	err := s.db.QueryRowContext(ctx, `SELECT value FROM alert_state WHERE key = ?`, key).Scan(&v)
	if err != nil && err != sql.ErrNoRows {
		return "", fmt.Errorf("store: alert state %s: %w", key, err)
	}
	return v, nil
}

// SetAlertState saves value under key for the alert engine.
func (s *Store) SetAlertState(ctx context.Context, key, value string) error {
	err := s.write(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `INSERT INTO alert_state(key, value) VALUES (?, ?)
			ON CONFLICT(key) DO UPDATE SET value = excluded.value`, key, value)
		return err
	})
	if err != nil {
		return fmt.Errorf("store: set alert state %s: %w", key, err)
	}
	return nil
}
