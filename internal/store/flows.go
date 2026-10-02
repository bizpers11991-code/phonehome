package store

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/bizpers11991-code/phonehome/internal/model"
)

// InsertFlows stores fs in one transaction.
func (s *Store) InsertFlows(ctx context.Context, fs []model.Flow) error {
	if len(fs) == 0 {
		return nil
	}
	s.wmu.Lock()
	defer s.wmu.Unlock()
	var lk *lookups
	err := inTx(ctx, s.db, func(tx *sql.Tx) error {
		var err error
		if lk, err = s.newLookups(ctx, tx); err != nil {
			return err
		}
		ins, err := tx.PrepareContext(ctx, `INSERT INTO flows
			(start_ts, end_ts, client, remote, remote_port, proto, bytes_out, bytes_in, source_id)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`)
		if err != nil {
			return err
		}
		for _, f := range fs {
			src, err := lk.source(ctx, f.Source)
			if err != nil {
				return err
			}
			// Byte counters are stored bit-for-bit as int64.
			if _, err := ins.ExecContext(ctx, nanos(f.Start), nanos(f.End), ipBytes(f.ClientIP), ipBytes(f.RemoteIP),
				f.RemotePort, f.Proto, int64(f.BytesOut), int64(f.BytesIn), src); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return fmt.Errorf("store: insert flows: %w", err)
	}
	lk.commit()
	return nil
}

// FlowsBetween returns the flows that started in p, oldest first.
func (s *Store) FlowsBetween(ctx context.Context, p model.Period) ([]model.Flow, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT f.start_ts, f.end_ts, f.client, f.remote, f.remote_port, f.proto, f.bytes_out, f.bytes_in, s.name
		FROM flows f JOIN sources s ON s.id = f.source_id
		WHERE f.start_ts >= ? AND f.start_ts < ?
		ORDER BY f.start_ts, f.id`, nanos(p.From), nanos(p.To))
	if err != nil {
		return nil, fmt.Errorf("store: flows between: %w", err)
	}
	defer rows.Close()
	var out []model.Flow
	for rows.Next() {
		var (
			start, end, out64, in64 int64
			client, remote          []byte
			f                       model.Flow
		)
		if err := rows.Scan(&start, &end, &client, &remote, &f.RemotePort, &f.Proto, &out64, &in64, &f.Source); err != nil {
			return nil, fmt.Errorf("store: flows between: %w", err)
		}
		f.Start, f.End = fromNanos(start), fromNanos(end)
		f.ClientIP, f.RemoteIP = ipFrom(client), ipFrom(remote)
		f.BytesOut, f.BytesIn = uint64(out64), uint64(in64)
		out = append(out, f)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store: flows between: %w", err)
	}
	return out, nil
}
