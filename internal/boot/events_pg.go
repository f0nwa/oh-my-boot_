package boot

import (
	"context"

	"github.com/jackc/pgx/v5/pgxpool"
)

type PostgresStore struct {
	Pool *pgxpool.Pool
}

func (s PostgresStore) Insert(ctx context.Context, event Event) error {
	_, err := s.Pool.Exec(ctx,
		"INSERT INTO boot_events (occurred_at, kind, path, remote_addr) VALUES ($1, $2, $3, $4)",
		event.At, event.Kind, event.Path, event.RemoteAddr,
	)
	return err
}
