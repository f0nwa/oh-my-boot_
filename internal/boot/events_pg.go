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
	if limit < 1 || limit > 100 {
		limit = 30
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	rows, err := s.Pool.Query(ctx, "SELECT occurred_at, kind, path, remote_addr FROM boot_events ORDER BY occurred_at DESC, id DESC LIMIT $1", limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	events := make([]Event, 0, limit)
	for rows.Next() {
		var e Event
		if err := rows.Scan(&e.At, &e.Kind, &e.Path, &e.RemoteAddr); err != nil {
			return nil, err
		}
		events = append(events, e)
	}
	return events, rows.Err()
}

func (s PostgresStore) Insert(ctx context.Context, event Event) error {
	_, err := s.Pool.Exec(ctx,
		"INSERT INTO boot_events (occurred_at, kind, path, remote_addr) VALUES ($1, $2, $3, $4)",
		event.At, event.Kind, event.Path, event.RemoteAddr,
	)
	return err
}
