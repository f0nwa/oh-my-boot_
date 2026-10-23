package boot

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

type PostgresStore struct {
	Pool *pgxpool.Pool
}

func (s PostgresStore) Recent(limit int) ([]Event, error) {
	if limit < 1 || limit > 1000 {
		limit = 30
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	rows, err := s.Pool.Query(ctx, "SELECT occurred_at, kind, path, remote_addr, mac, status, bytes, size FROM boot_events ORDER BY occurred_at DESC, id DESC LIMIT $1", limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	events := make([]Event, 0, limit)
	for rows.Next() {
		var e Event
		if err := rows.Scan(&e.At, &e.Kind, &e.Path, &e.RemoteAddr, &e.MAC, &e.Status, &e.Bytes, &e.Size); err != nil {
			return nil, err
		}
		events = append(events, e)
	}
	return events, rows.Err()
}

func (s PostgresStore) Insert(ctx context.Context, event Event) error {
	_, err := s.Pool.Exec(ctx,
		"INSERT INTO boot_events (occurred_at, kind, path, remote_addr, mac, status, bytes, size) VALUES ($1, $2, $3, $4, $5, $6, $7, $8)",
		event.At, event.Kind, event.Path, event.RemoteAddr, event.MAC, event.Status, event.Bytes, event.Size,
	)
	return err
}

// DeleteBefore removes requests logged before the given time.
func (s PostgresStore) DeleteBefore(ctx context.Context, before time.Time) (int64, error) {
	tag, err := s.Pool.Exec(ctx, "DELETE FROM boot_events WHERE occurred_at < $1", before)
	if err != nil {
		return 0, err
	}
	return tag.RowsAffected(), nil
}

// Stats counts the requests, finds the oldest and measures the table.
func (s PostgresStore) Stats(ctx context.Context) (JournalStats, error) {
	var stats JournalStats
	err := s.Pool.QueryRow(ctx, "SELECT count(*), min(occurred_at), pg_total_relation_size('boot_events') FROM boot_events").Scan(&stats.Count, &stats.Oldest, &stats.Bytes)
	return stats, err
}
