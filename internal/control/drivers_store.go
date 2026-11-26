package control

import (
	"context"
	"encoding/json"
	"time"

	"github.com/jackc/pgx/v5"
	"omb/internal/drivers"
)

type DriverPackage struct {
	SHA256      string
	Title       string
	Description string
	Author      string
	Inventory   drivers.Inventory
	CreatedAt   time.Time
}

// CreateDriverPackage adds a package to the catalog. It reports false when a
// package with the same content is already there.
func (s Store) CreateDriverPackage(ctx context.Context, inventory drivers.Inventory, title, description string) (bool, error) {
	data, err := json.Marshal(inventory)
	if err != nil {
		return false, err
	}
	command, err := s.Pool.Exec(ctx, `INSERT INTO winpe_driver_packages (sha256, title, description, inventory) VALUES ($1,$2,$3,$4) ON CONFLICT (sha256) DO NOTHING`, inventory.SHA256, title, description, data)
	if err != nil {
		return false, err
	}
	return command.RowsAffected() > 0, nil
}

func (s Store) DriverPackages(ctx context.Context) ([]DriverPackage, error) {
	rows, err := s.Pool.Query(ctx, `SELECT sha256, title, description, inventory, created_at FROM winpe_driver_packages ORDER BY created_at DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := []DriverPackage{}
	for rows.Next() {
		var item DriverPackage
		var data []byte
		if err := rows.Scan(&item.SHA256, &item.Title, &item.Description, &data, &item.CreatedAt); err != nil {
			return nil, err
		}
		if err := json.Unmarshal(data, &item.Inventory); err != nil {
			return nil, err
		}
		result = append(result, item)
	}
	return result, rows.Err()
}

func (s Store) UpdateDriverPackageMetadata(ctx context.Context, hash, title, description string) error {
	tag, err := s.Pool.Exec(ctx, `UPDATE winpe_driver_packages SET title=$2, description=$3 WHERE sha256=$1`, hash, title, description)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return pgx.ErrNoRows
	}
	return nil
}

func (s Store) DeleteDriverPackage(ctx context.Context, hash string) error {
	tag, err := s.Pool.Exec(ctx, `DELETE FROM winpe_driver_packages WHERE sha256=$1`, hash)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return pgx.ErrNoRows
	}
	return nil
}
